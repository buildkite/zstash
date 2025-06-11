package store

import "time"

type TransferInfo struct {
	BytesTransferred int64
	TransferSpeed    float64 // in MB/s
	RequestID        string
	Duration         time.Duration
}
