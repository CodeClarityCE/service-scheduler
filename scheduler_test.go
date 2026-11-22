package main

import (
	"testing"

	"github.com/CodeClarityCE/utility-boilerplates/testdeps"
)

func TestCreateSchedulerService(t *testing.T) {
	testdeps.ServiceDatabases(t)
	testdeps.RabbitMQ(t)

	service, err := CreateSchedulerService()
	if err != nil {
		t.Fatalf("create scheduler service: %v", err)
	}
	defer service.Close()

	if service.ServiceBase == nil {
		t.Error("Expected ServiceBase to be embedded, got nil")
	}
	if service.cron == nil {
		t.Error("Expected cron scheduler to be initialized, got nil")
	}
	if service.apiURL == "" {
		t.Error("Expected API URL to be set, got empty string")
	}
}
