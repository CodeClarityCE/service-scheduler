package main

import "testing"

func TestCreateSchedulerService(t *testing.T) {
	// Set test environment variables
	t.Setenv("PG_DB_HOST", "127.0.0.1")
	t.Setenv("PG_DB_PORT", "5432")
	t.Setenv("PG_DB_USER", "postgres")
	t.Setenv("PG_DB_PASSWORD", "!ChangeMe!")
	// Without a database, skip after 1s instead of retrying for the default 120s.
	t.Setenv("DB_CONNECT_MAX_WAIT_SECONDS", "1")

	// Test scheduler service creation
	service, err := CreateSchedulerService()
	if err != nil {
		t.Skipf("Skipping test due to database connection error: %v", err)
		return
	}
	defer service.Close()

	// Test that service was created successfully
	if service == nil {
		t.Error("Expected service to be created, got nil")
	}

	// Test that ServiceBase is properly embedded
	if service.ServiceBase == nil {
		t.Error("Expected ServiceBase to be embedded, got nil")
	}

	// Test that cron scheduler is initialized
	if service.cron == nil {
		t.Error("Expected cron scheduler to be initialized, got nil")
	}

	// Test that API URL is set
	if service.apiURL == "" {
		t.Error("Expected API URL to be set, got empty string")
	}
}
