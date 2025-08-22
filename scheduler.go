// Package main provides the entry point for the scheduler service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/CodeClarityCE/utility-types/boilerplates"
	"github.com/robfig/cron/v3"
	"github.com/uptrace/bun"
)

type ScheduledAnalysis struct {
	bun.BaseModel    `bun:"table:analysis"`
	ID               string                 `bun:"id,pk"`
	CreatedOn        time.Time              `bun:"created_on"`
	Config           map[string]interface{} `bun:"config,type:jsonb"`
	Stage            int                    `bun:"stage"`
	Status           string                 `bun:"status"`
	Steps            interface{}            `bun:"steps,type:jsonb"`
	StartedOn        *time.Time             `bun:"started_on"`
	EndedOn          *time.Time             `bun:"ended_on"`
	Branch           string                 `bun:"branch"`
	Tag              *string                `bun:"tag"`
	CommitHash       *string                `bun:"commit_hash"`
	ScheduleType     *string                `bun:"schedule_type"`
	NextScheduledRun *time.Time             `bun:"next_scheduled_run"`
	IsActive         bool                   `bun:"is_active"`
	LastScheduledRun *time.Time             `bun:"last_scheduled_run"`
	ProjectID        string                 `bun:"projectId"`
	AnalyzerID       string                 `bun:"analyzerId"`
	OrganizationID   string                 `bun:"organizationId"`
	IntegrationID    *string                `bun:"integrationId"`
	CreatedByID      string                 `bun:"createdById"`
}

func (ScheduledAnalysis) TableName() string {
	return "analysis"
}

// SchedulerService wraps the ServiceBase with scheduler-specific functionality
type SchedulerService struct {
	*boilerplates.ServiceBase
	cron   *cron.Cron
	apiURL string
}

// CreateSchedulerService creates a new SchedulerService
func CreateSchedulerService() (*SchedulerService, error) {
	base, err := boilerplates.CreateServiceBase()
	if err != nil {
		return nil, err
	}

	// Create cron scheduler
	c := cron.New(cron.WithSeconds())

	service := &SchedulerService{
		ServiceBase: base,
		cron:        c,
		apiURL:      "http://api:3000", // API connection for creating new analysis executions
	}

	return service, nil
}

func (s *SchedulerService) Start() {
	log.Println("Starting scheduler service...")

	// Add cron job to check for due analyses every minute
	_, err := s.cron.AddFunc("0 * * * * *", s.processDueAnalyses)
	if err != nil {
		log.Fatalf("Failed to add cron job: %v", err)
	}

	s.cron.Start()
	log.Println("Scheduler service started successfully")

	// Keep the service running
	select {}
}

func (s *SchedulerService) processDueAnalyses() {
	log.Println("Checking for due scheduled analyses...")

	ctx := context.Background()
	var analyses []ScheduledAnalysis

	// Find all due analyses
	err := s.DB.CodeClarity.NewSelect().
		Model(&analyses).
		Where("is_active = ?", true).
		Where("schedule_type IN (?)", bun.In([]string{"daily", "weekly"})).
		Where("next_scheduled_run <= ?", time.Now()).
		Scan(ctx)

	if err != nil {
		log.Printf("Error fetching due analyses: %v", err)
		return
	}

	log.Printf("Found %d due analyses", len(analyses))

	for _, analysis := range analyses {
		s.processAnalysis(analysis)
	}
}

func (s *SchedulerService) processAnalysis(analysis ScheduledAnalysis) {
	log.Printf("Processing scheduled analysis: %s", analysis.ID)

	// Create a new analysis execution to preserve historical results
	newAnalysisId, err := s.createAnalysisExecution(analysis)
	if err != nil {
		log.Printf("Failed to create new analysis execution for %s: %v", analysis.ID, err)
		return
	}

	log.Printf("Created new analysis execution: %s for scheduled analysis: %s", newAnalysisId, analysis.ID)

	// Send message to RabbitMQ to trigger the new analysis execution
	err = s.sendAnalysisMessage(analysis, newAnalysisId)
	if err != nil {
		log.Printf("Failed to send analysis message for %s: %v", newAnalysisId, err)
		return
	}

	// Update last run time and calculate next run time for the original scheduled analysis
	ctx := context.Background()
	now := time.Now()
	nextRun := s.calculateNextRun(analysis.ScheduleType, now)

	_, err = s.DB.CodeClarity.NewUpdate().
		Model((*ScheduledAnalysis)(nil)).
		Set("last_scheduled_run = ?", now).
		Set("next_scheduled_run = ?", nextRun).
		Where("id = ?", analysis.ID).
		Exec(ctx)

	if err != nil {
		log.Printf("Failed to update analysis schedule for %s: %v", analysis.ID, err)
		return
	}

	log.Printf("Successfully processed analysis %s, new execution: %s, next run: %s", analysis.ID, newAnalysisId, nextRun.Format(time.RFC3339))
}

func (s *SchedulerService) createAnalysisExecution(analysis ScheduledAnalysis) (string, error) {
	// Call the API to create a new analysis execution
	url := fmt.Sprintf("%s/org/%s/projects/%s/analyses/%s/execute",
		s.apiURL, analysis.OrganizationID, analysis.ProjectID, analysis.ID)

	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return "", fmt.Errorf("failed to call API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	return result.ID, nil
}

func (s *SchedulerService) sendAnalysisMessage(analysis ScheduledAnalysis, newAnalysisId string) error {
	// Create message in the format expected by dispatcher
	message := map[string]interface{}{
		"analysis_id":     newAnalysisId,
		"project_id":      analysis.ProjectID,
		"integration_id":  analysis.IntegrationID,
		"organization_id": analysis.OrganizationID,
		"config":          analysis.Config,
	}

	body, err := json.Marshal(message)
	if err != nil {
		return err
	}

	// Use ServiceBase's SendMessage method
	return s.SendMessage("api_request", body)
}

func (s *SchedulerService) calculateNextRun(scheduleType *string, from time.Time) time.Time {
	if scheduleType == nil {
		return from.Add(24 * time.Hour) // default to daily
	}

	switch *scheduleType {
	case "daily":
		return from.Add(24 * time.Hour)
	case "weekly":
		return from.Add(7 * 24 * time.Hour)
	default:
		return from.Add(24 * time.Hour) // default to daily
	}
}

func main() {
	service, err := CreateSchedulerService()
	if err != nil {
		log.Fatalf("Failed to create scheduler service: %v", err)
	}
	defer service.Close()

	log.Printf("Starting Scheduler Service...")
	service.Start()
}
