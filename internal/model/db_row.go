package model

import "time"

// ProcessRow is the database row model for the processes table.
type ProcessRow struct {
	ID             string
	PID            int
	Mode           string
	Status         string
	WorkflowSource string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MetadataJSON   string
}

// SessionRouteRow is the database row model for the session_routes table.
type SessionRouteRow struct {
	Namespace        string
	AgentID          string
	SessionKey       string
	CurrentSessionID string
	UpdatedAt        time.Time
}

// MediaEntry is the metadata for a stored media file.
type MediaEntry struct {
	ID          string    `json:"id"`
	MIMEType    string    `json:"mimeType"`
	Size        int64     `json:"size"`
	StoragePath string    `json:"-"` // local file path; not serialized to API
	ExpiresAt   time.Time `json:"expiresAt"`
	CreatedAt   time.Time `json:"createdAt"`
}
