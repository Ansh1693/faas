package lambda

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ansh1693/faas/internal"
	"github.com/google/uuid"
)

const (
	defaultHealthTimeout = 10 * time.Second
	defaultInvokeTimeout = 30 * time.Second
	defaultMaxContainers = 20
	defaultRecycleEvery  = 50
	defaultAcquireWait   = 15 * time.Second
	defaultIdleTimeout   = 15 * time.Minute
	defaultSweepInterval = 30 * time.Second
)

var (
	ErrRuntimeUnsupported = errors.New("runtime is not supported yet")
	ErrWarmPoolExhausted  = errors.New("warm pool exhausted")
	uuidMatcher           = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

type Invoker interface {
	BuildImage(ctx context.Context, fn internal.Function) (string, error)
	Invoke(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (*internal.TriggerInvocationResponse, error)
	InvokeRaw(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (json.RawMessage, error)
}

type LogWriter interface {
	InsertFunctionLogs(ctx context.Context, logs []internal.FunctionLog) error
}

type Manager struct {
	httpClient     *http.Client
	maxContainers  int
	recycleEvery   int
	acquireWait    time.Duration
	idleTimeout    time.Duration
	sweepInterval  time.Duration
	defaultCPUs    string
	defaultPIDs    int
	readOnlyFS     bool
	networkName    string
	containerSlots chan struct{}
	logWriter      LogWriter
	poolsMu        sync.Mutex
	pools          map[string]*warmPool
	startOnce      sync.Once
	networkOnce    sync.Once
	networkErr     error
	shutdownOnce   sync.Once
}

type warmContainer struct {
	id          string
	hostPort    string
	invocations int
	lastUsedAt  time.Time
	lastLogAt   time.Time
	imageName   string
}

type warmPool struct {
	fn         internal.Function
	containers chan *warmContainer
	mu         sync.Mutex
	active     int
}

func NewManager(logWriter LogWriter) *Manager {
	maxContainers := envIntOrDefault("MAX_CONCURRENT_CONTAINERS", defaultMaxContainers)
	recycleEvery := envIntOrDefault("WARM_CONTAINER_RECYCLE_INVOCATIONS", defaultRecycleEvery)
	acquireWaitMS := envIntOrDefault("WARM_POOL_ACQUIRE_TIMEOUT_MS", int(defaultAcquireWait/time.Millisecond))
	idleTimeoutMS := envIntOrDefault("WARM_CONTAINER_IDLE_TIMEOUT_MS", int(defaultIdleTimeout/time.Millisecond))
	sweepMS := envIntOrDefault("WARM_CONTAINER_SWEEP_INTERVAL_MS", int(defaultSweepInterval/time.Millisecond))
	if maxContainers < 1 {
		maxContainers = defaultMaxContainers
	}
	if recycleEvery < 1 {
		recycleEvery = defaultRecycleEvery
	}
	if acquireWaitMS < 1000 {
		acquireWaitMS = int(defaultAcquireWait / time.Millisecond)
	}
	if idleTimeoutMS < 1000 {
		idleTimeoutMS = int(defaultIdleTimeout / time.Millisecond)
	}
	if sweepMS < 1000 {
		sweepMS = int(defaultSweepInterval / time.Millisecond)
	}
	return &Manager{
		httpClient:     &http.Client{},
		maxContainers:  maxContainers,
		recycleEvery:   recycleEvery,
		acquireWait:    time.Duration(acquireWaitMS) * time.Millisecond,
		idleTimeout:    time.Duration(idleTimeoutMS) * time.Millisecond,
		sweepInterval:  time.Duration(sweepMS) * time.Millisecond,
		defaultCPUs:    strings.TrimSpace(getEnvOrDefault("DEFAULT_FUNCTION_CPUS", "0.5")),
		defaultPIDs:    envIntOrDefault("DEFAULT_FUNCTION_PIDS_LIMIT", 256),
		readOnlyFS:     envBoolOrDefault("CONTAINER_READ_ONLY_FS", true),
		networkName:    strings.TrimSpace(getEnvOrDefault("FAAS_DOCKER_NETWORK", "faas-net")),
		containerSlots: make(chan struct{}, maxContainers),
		logWriter:      logWriter,
		pools:          make(map[string]*warmPool),
	}
}

func (m *Manager) BuildImage(ctx context.Context, fn internal.Function) (string, error) {
	switch strings.ToLower(strings.TrimSpace(fn.Runtime)) {
	case "nodejs22":
		return m.buildNodeImage(ctx, fn)
	default:
		return "", ErrRuntimeUnsupported
	}
}

func (m *Manager) Start(ctx context.Context) {
	m.startOnce.Do(func() {
		go m.sweeperLoop(ctx)
	})
}

func (m *Manager) Shutdown(ctx context.Context) {
	m.shutdownOnce.Do(func() {
		ids, _ := m.listManagedContainerIDs(ctx)
		for _, id := range ids {
			m.forceRemoveContainer(ctx, id)
		}
	})
}

func (m *Manager) Invoke(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (*internal.TriggerInvocationResponse, error) {
	raw, err := m.InvokeRaw(ctx, fn, payload)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &internal.TriggerInvocationResponse{}, nil
	}
	var out internal.TriggerInvocationResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) InvokeRaw(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (json.RawMessage, error) {
	if strings.ToLower(strings.TrimSpace(fn.ContainerStrategy)) == "warm" {
		return m.invokeWarm(ctx, fn, payload)
	}
	return m.invokeCold(ctx, fn, payload)
}

func (m *Manager) invokeCold(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (json.RawMessage, error) {
	if err := m.acquireSlot(ctx); err != nil {
		return nil, err
	}
	containerID, hostPort, err := m.startContainer(ctx, fn)
	if err != nil {
		m.releaseSlot()
		return nil, err
	}
	since := time.Now()
	defer func() {
		m.forceRemoveContainer(context.Background(), containerID)
		m.releaseSlot()
	}()
	if err := m.waitForHealth(ctx, hostPort); err != nil {
		return nil, err
	}
	resp, err := m.invokeHTTP(ctx, fn, hostPort, payload)
	go m.captureAndPersistLogs(context.Background(), fn.Name, payload.InvocationID, containerID, since)
	return resp, err
}

func (m *Manager) invokeWarm(ctx context.Context, fn internal.Function, payload internal.TriggerPayload) (json.RawMessage, error) {
	pool := m.ensurePool(fn)
	acquireCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		acquireCtx, cancel = context.WithTimeout(ctx, m.acquireWait)
		defer cancel()
	}
	container, err := m.acquireWarmContainer(acquireCtx, pool)
	if err != nil {
		return nil, err
	}

	resp, invokeErr := m.invokeHTTP(ctx, fn, container.hostPort, payload)
	go m.captureAndPersistLogs(context.Background(), fn.Name, payload.InvocationID, container.id, container.lastLogAt)
	container.lastLogAt = time.Now()
	container.lastUsedAt = time.Now()
	container.invocations++
	containerIsStale := container.imageName != pool.fn.ImageName

	if invokeErr != nil {
		m.removeWarmContainer(context.Background(), pool, container)
		m.spawnWarmContainerAsync(pool)
		return nil, invokeErr
	}
	if containerIsStale {
		m.removeWarmContainer(context.Background(), pool, container)
		m.spawnWarmContainerAsync(pool)
		return resp, nil
	}
	if container.invocations >= m.recycleEvery {
		m.removeWarmContainer(context.Background(), pool, container)
		m.spawnWarmContainerAsync(pool)
	} else {
		select {
		case pool.containers <- container:
		case <-ctx.Done():
			m.removeWarmContainer(context.Background(), pool, container)
		}
	}
	return resp, nil
}

func (m *Manager) ensurePool(fn internal.Function) *warmPool {
	key := fn.Name
	m.poolsMu.Lock()
	defer m.poolsMu.Unlock()
	if existing, ok := m.pools[key]; ok {
		existing.mu.Lock()
		existing.fn = fn
		existing.mu.Unlock()
		return existing
	}
	size := fn.WarmPoolSize
	if size < 1 {
		size = 1
	}
	pool := &warmPool{fn: fn, containers: make(chan *warmContainer, size)}
	m.pools[key] = pool
	for i := 0; i < size; i++ {
		m.spawnWarmContainerAsync(pool)
	}
	return pool
}

func (m *Manager) spawnWarmContainerAsync(pool *warmPool) {
	go func() {
		pool.mu.Lock()
		if pool.active >= cap(pool.containers) {
			pool.mu.Unlock()
			return
		}
		fn := pool.fn
		pool.mu.Unlock()
		if err := m.acquireSlot(context.Background()); err != nil {
			return
		}
		containerID, hostPort, err := m.startContainer(context.Background(), fn)
		if err != nil {
			m.releaseSlot()
			return
		}
		if err := m.waitForHealth(context.Background(), hostPort); err != nil {
			m.forceRemoveContainer(context.Background(), containerID)
			m.releaseSlot()
			return
		}
		now := time.Now()
		w := &warmContainer{id: containerID, hostPort: hostPort, lastUsedAt: now, lastLogAt: now, imageName: fn.ImageName}
		pool.mu.Lock()
		pool.active++
		pool.mu.Unlock()
		select {
		case pool.containers <- w:
		default:
			m.removeWarmContainer(context.Background(), pool, w)
		}
	}()
}

func (m *Manager) acquireWarmContainer(ctx context.Context, pool *warmPool) (*warmContainer, error) {
	select {
	case c := <-pool.containers:
		return c, nil
	default:
	}
	pool.mu.Lock()
	active := pool.active
	capacity := cap(pool.containers)
	fn := pool.fn
	pool.mu.Unlock()
	if active < capacity {
		if err := m.acquireSlot(ctx); err != nil {
			return nil, err
		}
		containerID, hostPort, err := m.startContainer(ctx, fn)
		if err != nil {
			m.releaseSlot()
			return nil, err
		}
		if err := m.waitForHealth(ctx, hostPort); err != nil {
			m.forceRemoveContainer(context.Background(), containerID)
			m.releaseSlot()
			return nil, err
		}
		pool.mu.Lock()
		pool.active++
		pool.mu.Unlock()
		now := time.Now()
		return &warmContainer{id: containerID, hostPort: hostPort, lastUsedAt: now, lastLogAt: now, imageName: fn.ImageName}, nil
	}
	select {
	case c := <-pool.containers:
		return c, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrWarmPoolExhausted
		}
		return nil, ctx.Err()
	}
}

func (m *Manager) removeWarmContainer(ctx context.Context, pool *warmPool, c *warmContainer) {
	m.forceRemoveContainer(ctx, c.id)
	m.releaseSlot()
	pool.mu.Lock()
	if pool.active > 0 {
		pool.active--
	}
	pool.mu.Unlock()
}

func (m *Manager) invokeHTTP(ctx context.Context, fn internal.Function, hostPort string, payload internal.TriggerPayload) (json.RawMessage, error) {
	invokeTimeout := time.Duration(fn.TimeoutSeconds) * time.Second
	if invokeTimeout <= 0 {
		invokeTimeout = defaultInvokeTimeout
	}
	invokeCtx, cancel := context.WithTimeout(ctx, invokeTimeout)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(invokeCtx, http.MethodPost, "http://127.0.0.1:"+hostPort+"/invoke", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody := bytes.Buffer{}
	_, _ = respBody.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("function returned status=%d body=%s", resp.StatusCode, respBody.String())
	}
	if strings.TrimSpace(respBody.String()) == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(respBody.Bytes()), nil
}

func (m *Manager) buildNodeImage(ctx context.Context, fn internal.Function) (string, error) {
	if strings.TrimSpace(fn.CodePath) == "" {
		return "", errors.New("code_path is required")
	}
	imageName := strings.TrimSpace(fn.ImageName)
	if imageName == "" {
		imageName = "lambda-fn-" + sanitizeTag(fn.Name) + ":latest"
	}
	bootstrapPath := filepath.Join(fn.CodePath, ".evtq_bootstrap.js")
	dockerfilePath := filepath.Join(fn.CodePath, ".evtq.Dockerfile")
	if err := os.WriteFile(bootstrapPath, []byte(nodeBootstrapJS), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(bootstrapPath) //nolint:errcheck
	df := `FROM node:22-alpine
WORKDIR /var/task
COPY . /var/task
RUN if [ -f package.json ]; then npm install --omit=dev; fi
EXPOSE 8080
CMD ["node", "/var/task/.evtq_bootstrap.js"]
`
	if err := os.WriteFile(dockerfilePath, []byte(df), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(dockerfilePath) //nolint:errcheck
	cmd := exec.CommandContext(ctx, "docker", "build", "-f", dockerfilePath, "-t", imageName, fn.CodePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker build failed: %w output=%s", err, string(out))
	}
	return imageName, nil
}

func (m *Manager) startContainer(ctx context.Context, fn internal.Function) (string, string, error) {
	if err := m.ensureNetwork(ctx); err != nil {
		return "", "", err
	}
	mem := fn.MemoryMB
	if mem <= 0 {
		mem = 128
	}
	timeoutMS := strconv.Itoa(fn.TimeoutSeconds * 1000)
	if fn.TimeoutSeconds <= 0 {
		timeoutMS = "30000"
	}
	args := []string{
		"run", "-d", "--rm",
		"--label", "faas.managed=true",
		"--label", "faas.function=" + fn.Name,
		"--network", m.networkName,
		"-p", "127.0.0.1::8080",
		"-m", fmt.Sprintf("%dm", mem),
		"--cpus", m.defaultCPUs,
		"--pids-limit", strconv.Itoa(m.defaultPIDs),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}
	if m.readOnlyFS {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m")
	}
	args = append(args, "-e", "HANDLER="+fn.Handler, "-e", "FUNCTION_NAME="+fn.Name, "-e", "FUNCTION_TIMEOUT_MS="+timeoutMS)
	for k, v := range fn.Environment {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, fn.ImageName)
	run := exec.CommandContext(ctx, "docker", args...)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("docker run failed: %w output=%s", err, string(out))
	}
	id := strings.TrimSpace(string(out))
	portCmd := exec.CommandContext(ctx, "docker", "port", id, "8080/tcp")
	portOut, err := portCmd.CombinedOutput()
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(strings.TrimSpace(string(portOut)), ":")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected docker port output: %s", strings.TrimSpace(string(portOut)))
	}
	return id, strings.TrimSpace(parts[len(parts)-1]), nil
}

func (m *Manager) waitForHealth(ctx context.Context, port string) error {
	deadline := time.Now().Add(defaultHealthTimeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/health", nil)
		resp, err := m.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("container health check timeout")
}

func (m *Manager) acquireSlot(ctx context.Context) error {
	select {
	case m.containerSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) releaseSlot() {
	select {
	case <-m.containerSlots:
	default:
	}
}

func (m *Manager) forceRemoveContainer(ctx context.Context, containerID string) {
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
}

func (m *Manager) sweeperLoop(ctx context.Context) {
	ticker := time.NewTicker(m.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweepIdleContainers()
		}
	}
}

func (m *Manager) sweepIdleContainers() {
	now := time.Now()
	m.poolsMu.Lock()
	pools := make([]*warmPool, 0, len(m.pools))
	for _, pool := range m.pools {
		pools = append(pools, pool)
	}
	m.poolsMu.Unlock()

	for _, pool := range pools {
		kept := make([]*warmContainer, 0)
		for {
			select {
			case c := <-pool.containers:
				if now.Sub(c.lastUsedAt) > m.idleTimeout {
					m.removeWarmContainer(context.Background(), pool, c)
					go m.spawnWarmContainerAsync(pool)
					continue
				}
				kept = append(kept, c)
			default:
				for _, c := range kept {
					pool.containers <- c
				}
				goto nextPool
			}
		}
	nextPool:
	}
}

func (m *Manager) ensureNetwork(ctx context.Context) error {
	m.networkOnce.Do(func() {
		check := exec.CommandContext(ctx, "docker", "network", "inspect", m.networkName)
		if err := check.Run(); err == nil {
			return
		}
		create := exec.CommandContext(ctx, "docker", "network", "create", m.networkName)
		if out, err := create.CombinedOutput(); err != nil {
			m.networkErr = fmt.Errorf("create docker network failed: %w output=%s", err, string(out))
		}
	})
	return m.networkErr
}

func (m *Manager) captureAndPersistLogs(ctx context.Context, functionName string, invocationID uuid.UUID, containerID string, since time.Time) {
	logCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lines := m.collectContainerLogs(logCtx, containerID, since)
	if len(lines) == 0 || m.logWriter == nil {
		return
	}
	inv := invocationID
	entries := make([]internal.FunctionLog, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		resolvedInvocation := inv
		if parsed := extractInvocationID(line.text); parsed != nil {
			resolvedInvocation = *parsed
		}
		entries = append(entries, internal.FunctionLog{
			FunctionName: functionName,
			InvocationID: &resolvedInvocation,
			Stream:       line.stream,
			LogLine:      line.text,
		})
	}
	if len(entries) == 0 {
		return
	}
	_ = m.logWriter.InsertFunctionLogs(logCtx, entries)
}

type dockerLogLine struct {
	stream string
	text   string
}

func (m *Manager) collectContainerLogs(ctx context.Context, containerID string, since time.Time) []dockerLogLine {
	sinceArg := since.UTC().Format(time.RFC3339Nano)
	cmd := exec.CommandContext(ctx, "docker", "logs", "--timestamps", "--since", sinceArg, containerID)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	out := make([]dockerLogLine, 0)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			line = parts[1]
		}
		out = append(out, dockerLogLine{stream: "combined", text: line})
	}
	return out
}

func (m *Manager) listManagedContainerIDs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label=faas.managed=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			ids = append(ids, l)
		}
	}
	return ids, nil
}

// ApplyFunctionUpdate refreshes warm pool config and restarts idle containers.
func (m *Manager) ApplyFunctionUpdate(fn internal.Function) {
	m.poolsMu.Lock()
	pool, ok := m.pools[fn.Name]
	m.poolsMu.Unlock()
	if !ok {
		return
	}
	pool.mu.Lock()
	pool.fn = fn
	pool.mu.Unlock()
	removed := m.removeAllIdleFromPool(pool)
	for i := 0; i < removed; i++ {
		m.spawnWarmContainerAsync(pool)
	}
}

// RemoveFunction removes function pool and force-stops idle warm containers.
func (m *Manager) RemoveFunction(functionName string) {
	m.poolsMu.Lock()
	pool, ok := m.pools[functionName]
	if ok {
		delete(m.pools, functionName)
	}
	m.poolsMu.Unlock()
	if !ok {
		return
	}
	_ = m.removeAllIdleFromPool(pool)
}

func (m *Manager) removeAllIdleFromPool(pool *warmPool) int {
	removed := 0
	for {
		select {
		case c := <-pool.containers:
			m.removeWarmContainer(context.Background(), pool, c)
			removed++
		default:
			return removed
		}
	}
}

func extractInvocationID(line string) *uuid.UUID {
	// Fast path for handler logs: "inv=<uuid>"
	if idx := strings.Index(line, "inv="); idx >= 0 {
		candidate := strings.TrimSpace(line[idx+4:])
		if end := strings.IndexAny(candidate, " \t,;]})\""); end >= 0 {
			candidate = candidate[:end]
		}
		if id, err := uuid.Parse(candidate); err == nil {
			return &id
		}
	}
	// Fallback for JSON event logs: match first UUID in line.
	if matched := uuidMatcher.FindString(line); matched != "" {
		if id, err := uuid.Parse(matched); err == nil {
			return &id
		}
	}
	return nil
}

func sanitizeTag(name string) string {
	replacer := strings.NewReplacer(" ", "-", "/", "-", ":", "-", "@", "-", "\\", "-")
	return strings.ToLower(replacer.Replace(name))
}

func envIntOrDefault(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func envBoolOrDefault(name string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes"
}

func getEnvOrDefault(name, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return raw
}

const nodeBootstrapJS = `
const http = require("http");
function resolveHandler(handlerSpec) {
  const spec = handlerSpec || "index.handler";
  const split = spec.lastIndexOf(".");
  if (split <= 0 || split >= spec.length - 1) {
    throw new Error("invalid HANDLER format, expected file.export (e.g. index.handler)");
  }
  const modulePath = spec.slice(0, split);
  const exportName = spec.slice(split + 1);
  const mod = require("/var/task/" + modulePath);
  const fn = mod[exportName];
  if (typeof fn !== "function") {
    throw new Error("handler export is not a function: " + spec);
  }
  return fn;
}
const handler = resolveHandler(process.env.HANDLER);
const timeoutMs = Number(process.env.FUNCTION_TIMEOUT_MS || "30000");
const functionName = process.env.FUNCTION_NAME || "unknown";
function parseBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", chunk => data += chunk);
    req.on("end", () => {
      if (!data) return resolve({});
      try { resolve(JSON.parse(data)); } catch (err) { reject(err); }
    });
    req.on("error", reject);
  });
}
const server = http.createServer(async (req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200, {"Content-Type": "application/json"});
    res.end(JSON.stringify({status: "ok"}));
    return;
  }
  if (req.method !== "POST" || req.url !== "/invoke") {
    res.writeHead(404, {"Content-Type": "application/json"});
    res.end(JSON.stringify({error: "not found"}));
    return;
  }
  let event;
  try { event = await parseBody(req); } catch (err) {
    res.writeHead(400, {"Content-Type": "application/json"});
    res.end(JSON.stringify({error: "invalid JSON", detail: String(err)}));
    return;
  }
  const invocationId = event && event.invocation_id ? event.invocation_id : "unknown";
  const context = { invocation_id: invocationId, timeout_ms: timeoutMs, function_name: functionName };
  try {
    const result = await Promise.resolve(handler(event, context));
    res.writeHead(200, {"Content-Type": "application/json"});
    if (typeof result === "undefined") { res.end("{}"); return; }
    res.end(JSON.stringify(result));
  } catch (err) {
    res.writeHead(500, {"Content-Type": "application/json"});
    res.end(JSON.stringify({error: "handler failed", detail: String(err && err.stack ? err.stack : err)}));
  }
});
server.listen(8080, "0.0.0.0");
`
