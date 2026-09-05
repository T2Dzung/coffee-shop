package config

import (
	"fmt"
	"strings"
)

const (
	StateEncryptionKMS = "sse-kms"
	StateEncryptionS3  = "sse-s3"
)

func normalizeStateEncryption(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return StateEncryptionKMS
	}
	return value
}

func validateStateEncryption(value string) error {
	if value != StateEncryptionKMS && value != StateEncryptionS3 {
		return fmt.Errorf("state encryption must be %s or %s", StateEncryptionKMS, StateEncryptionS3)
	}
	return nil
}

func UsesStateKMS(value string) bool {
	return value == StateEncryptionKMS
}
