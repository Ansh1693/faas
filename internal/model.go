package internal

import (
	"time"

	"github.com/google/uuid"
)

type Function struct {
	ID                uuid.UUID         `json:"id"`
	Name              string            `json:"name"`
	Runtime           string            `json:"runtime"`
	Handler           string            `json:"handler"`
	TimeoutSeconds    int               `json:"timeout_seconds"`
	MemoryMB          int               `json:"memory_mb"`
	Environment       map[string]string `json:"environment"`
	CodePath          string            `json:"code_path"`
	ContainerStrategy string            `json:"container_strategy"`
	WarmPoolSize      int               `json:"warm_pool_size"`
	ImageName         string            `json:"image_name"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	DeletedAt         *time.Time        `json:"deleted_at,omitempty"`
}

type TriggerRecord struct {
	MessageID               uuid.UUID              `json:"message_id"`
	ReceiptHandle           uuid.UUID              `json:"receipt_handle"`
	Body                    string                 `json:"body"`
	Attributes              map[string]interface{} `json:"attributes"`
	MessageGroupID          *string                `json:"message_group_id,omitempty"`
	ApproximateReceiveCount int                    `json:"approximate_receive_count"`
}

type TriggerPayload struct {
	TriggerID    uuid.UUID       `json:"trigger_id"`
	InvocationID uuid.UUID       `json:"invocation_id"`
	QueueID      uuid.UUID       `json:"queue_id"`
	QueueName    string          `json:"queue_name"`
	QueueType    string          `json:"queue_type"`
	SourceQueue  string          `json:"source_queue"`
	Target       string          `json:"target"`
	Records      []TriggerRecord `json:"records"`
}

type BatchItemFailure struct {
	ItemIdentifier uuid.UUID `json:"item_identifier"`
}

type TriggerInvocationResponse struct {
	BatchItemFailures []BatchItemFailure `json:"batch_item_failures,omitempty"`
}

type FunctionLog struct {
	FunctionName string     `json:"function_name"`
	InvocationID *uuid.UUID `json:"invocation_id,omitempty"`
	Stream       string     `json:"stream"`
	LogLine      string     `json:"log_line"`
	LoggedAt     time.Time  `json:"logged_at"`
}
