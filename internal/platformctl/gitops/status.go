package gitops

import (
	"encoding/json"
	"fmt"
)

type Application struct {
	Status struct {
		Sync struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		Conditions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

func Evaluate(data []byte) error {
	var application Application
	if err := json.Unmarshal(data, &application); err != nil {
		return fmt.Errorf("decode Argo Application: %w", err)
	}
	if application.Status.Sync.Status != "Synced" || application.Status.Health.Status != "Healthy" {
		return fmt.Errorf("Argo Application is sync=%s health=%s", application.Status.Sync.Status, application.Status.Health.Status)
	}
	for _, condition := range application.Status.Conditions {
		switch condition.Type {
		case "ComparisonError", "InvalidSpecError", "SyncError":
			return fmt.Errorf("Argo blocking condition %s: %s", condition.Type, condition.Message)
		}
	}
	return nil
}
