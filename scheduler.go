// Package main provides the entry point for the scheduler service.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/robfig/cron/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

type ScheduledAnalysis struct {
	bun.BaseModel     `bun:"table:analysis"`
	ID                string                 `bun:"id,pk"`
	CreatedOn         time.Time              `bun:"created_on"`
	Config            map[string]interface{} `bun:"config,type:jsonb"`
	Stage             int                    `bun:"stage"`
	Status            string                 `bun:"status"`
	Steps             interface{}            `bun:"steps,type:jsonb"`
	StartedOn         *time.Time             `bun:"started_on"`
	EndedOn           *time.Time             `bun:"ended_on"`
	Branch            string                 `bun:"branch"`
	Tag               *string                `bun:"tag"`
	CommitHash        *string                `bun:"commit_hash"`
	ScheduleType      *string                `bun:"schedule_type"`
	NextScheduledRun  *time.Time             `bun:"next_scheduled_run"`
	IsActive          bool                   `bun:"is_active"`
	LastScheduledRun  *time.Time             `bun:"last_scheduled_run"`
	ProjectID         string                 `bun:"projectId"`
	AnalyzerID        string                 `bun:"analyzerId"`
	OrganizationID    string                 `bun:"organizationId"`
	IntegrationID     *string                `bun:"integrationId"`
	CreatedByID       string                 `bun:"createdById"`
}

func (ScheduledAnalysis) TableName() string {
	return "analysis"
}

type Scheduler struct {
	db      *bun.DB
	amqpURL string
	queue   string
	cron    *cron.Cron
	apiURL  string
}

func NewScheduler() *Scheduler {
	// Database connection
	host := getEnv("PG_DB_HOST", "localhost")
	port := getEnv("PG_DB_PORT", "6432")
	user := getEnv("PG_DB_USER", "postgres")
	password := getEnv("PG_DB_PASSWORD", "password")
	dbname := getEnv("PG_DB_NAME", "codeclarity")

	dsn := "postgres://" + user + ":" + password + "@" + host + ":" + port + "/" + dbname + "?sslmode=disable"
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	db := bun.NewDB(sqldb, pgdialect.New())

	// AMQP connection
	protocol := getEnv("AMQP_PROTOCOL", "amqp")
	amqpHost := getEnv("AMQP_HOST", "localhost")
	amqpPort := getEnv("AMQP_PORT", "5672")
	amqpUser := getEnv("AMQP_USER", "guest")
	amqpPassword := getEnv("AMQP_PASSWORD", "guest")
	amqpURL := protocol + "://" + amqpUser + ":" + amqpPassword + "@" + amqpHost + ":" + amqpPort

	queue := getEnv("AMQP_ANALYSES_QUEUE", "api_request")

	// API connection for creating new analysis executions
	apiURL := getEnv("API_BASE_URL", "http://localhost:3000")

	// Create cron scheduler
	c := cron.New(cron.WithSeconds())

	return &Scheduler{
		db:      db,
		amqpURL: amqpURL,
		queue:   queue,
		cron:    c,
		apiURL:  apiURL,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func (s *Scheduler) Start() {
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

func (s *Scheduler) processDueAnalyses() {
	log.Println("Checking for due scheduled analyses...")

	ctx := context.Background()
	var analyses []ScheduledAnalysis

	// Find all due analyses
	err := s.db.NewSelect().
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

func (s *Scheduler) processAnalysis(analysis ScheduledAnalysis) {
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

	_, err = s.db.NewUpdate().
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

func (s *Scheduler) createAnalysisExecution(analysis ScheduledAnalysis) (string, error) {
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

func (s *Scheduler) sendAnalysisMessage(analysis ScheduledAnalysis, newAnalysisId string) error {
	conn, err := amqp.Dial(s.amqpURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	_, err = ch.QueueDeclare(
		s.queue, // name
		true,    // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		return err
	}

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

	return ch.Publish(
		"",      // exchange
		s.queue, // routing key
		false,   // mandatory
		false,   // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})
}

func (s *Scheduler) calculateNextRun(scheduleType *string, from time.Time) time.Time {
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
	scheduler := NewScheduler()
	scheduler.Start()
}