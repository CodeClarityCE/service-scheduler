// Package main provides the entry point for the scheduler service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CodeClarityCE/utility-boilerplates"
	codeclarity "github.com/CodeClarityCE/utility-types/codeclarity_db"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/uptrace/bun"
)

// ScheduledAnalysis is the scheduler's view of the analysis table, whose schema the API owns.
type ScheduledAnalysis struct {
	bun.BaseModel    `bun:"table:analysis"`
	ID               string         `bun:"id,pk,nullzero"`
	CreatedOn        time.Time      `bun:"created_on"`
	Config           map[string]any `bun:"config,type:jsonb"`
	Stage            int            `bun:"stage"`
	Status           string         `bun:"status"`
	Steps            any            `bun:"steps,type:jsonb"`
	StartedOn        *time.Time     `bun:"started_on"`
	EndedOn          *time.Time     `bun:"ended_on"`
	Branch           string         `bun:"branch"`
	Tag              *string        `bun:"tag"`
	CommitHash       *string        `bun:"commit_hash"`
	ScheduleType     *string        `bun:"schedule_type"`
	NextScheduledRun *time.Time     `bun:"next_scheduled_run"`
	IsActive         bool           `bun:"is_active"`
	LastScheduledRun *time.Time     `bun:"last_scheduled_run"`
	ProjectID        string         `bun:"projectId,nullzero"`
	AnalyzerID       string         `bun:"analyzerId,nullzero"`
	OrganizationID   string         `bun:"organizationId,nullzero"`
	IntegrationID    *string        `bun:"integrationId"`
	CreatedByID      string         `bun:"createdById,nullzero"`
}

func (ScheduledAnalysis) TableName() string {
	return "analysis"
}

// Schedule types the scheduler runs; "once" analyses are runs, not schedules.
var recurringTypes = []string{"daily", "weekly"}

// How long one tick may take, so a hung database does not stack up ticks.
const tickTimeout = 50 * time.Second

// SchedulerService wraps the ServiceBase with scheduler-specific functionality
type SchedulerService struct {
	*boilerplates.ServiceBase
	cron    *cron.Cron
	db      *bun.DB
	publish func(queue string, body []byte) error
	now     func() time.Time
}

// CreateSchedulerService creates a new SchedulerService
func CreateSchedulerService() (*SchedulerService, error) {
	base, err := boilerplates.CreateServiceBase()
	if err != nil {
		return nil, err
	}

	service := &SchedulerService{
		ServiceBase: base,
		cron:        cron.New(cron.WithSeconds()),
		db:          base.DB.CodeClarity,
		publish:     base.SendMessage,
		now:         time.Now,
	}

	return service, nil
}

func (s *SchedulerService) Start() {
	logrus.Info("Starting scheduler service")

	// Check for due analyses every minute
	_, err := s.cron.AddFunc("0 * * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
		defer cancel()
		s.processDueAnalyses(ctx)
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to add cron job")
	}

	s.cron.Start()
	logrus.Info("Scheduler service started")

	// Keep the service running
	select {}
}

// dueAnalyses returns the active recurring analyses whose next run is due.
func (s *SchedulerService) dueAnalyses(ctx context.Context) ([]ScheduledAnalysis, error) {
	var analyses []ScheduledAnalysis
	err := s.db.NewSelect().
		Model(&analyses).
		Where("is_active = ?", true).
		Where("schedule_type IN (?)", bun.List(recurringTypes)).
		Where("next_scheduled_run <= ?", s.now()).
		Scan(ctx)
	return analyses, err
}

func (s *SchedulerService) processDueAnalyses(ctx context.Context) {
	analyses, err := s.dueAnalyses(ctx)
	if err != nil {
		logrus.WithError(err).Error("Failed to fetch due scheduled analyses")
		return
	}

	logrus.WithField("count", len(analyses)).Debug("Found due scheduled analyses")

	for _, analysis := range analyses {
		s.processAnalysis(ctx, analysis)
	}
}

// processAnalysis creates one run for a due scheduled analysis and moves its schedule forward.
// The schedule moves forward even when the run cannot be created, so a failure is not retried every minute.
func (s *SchedulerService) processAnalysis(ctx context.Context, analysis ScheduledAnalysis) {
	log := logrus.WithFields(logrus.Fields{
		"analysis_id":     analysis.ID,
		"project_id":      analysis.ProjectID,
		"organization_id": analysis.OrganizationID,
		"user_id":         analysis.CreatedByID,
	})

	now := s.now()
	nextRun := calculateNextRun(analysis.ScheduleType, now)

	runID, claimed, insertErr, err := s.claimAndCreateRun(ctx, analysis, now, nextRun)
	if err != nil {
		log.WithError(err).Error("Failed to claim scheduled analysis")
		return
	}
	if !claimed {
		log.Debug("Scheduled analysis already handled for this due time")
		return
	}
	if insertErr != nil {
		log.WithError(insertErr).Error("Failed to insert scheduled run; skipping to the next due time")
		return
	}
	log = log.WithField("run_id", runID)

	if err := s.sendAnalysisMessage(analysis, runID); err != nil {
		log.WithError(err).Error("Failed to publish scheduled run")
		s.failRun(ctx, log, runID)
		return
	}

	log.WithField("next_run", nextRun.Format(time.RFC3339)).Info("Created scheduled run")
}

// claimAndCreateRun moves the schedule forward and inserts the new run in one transaction.
// Only one caller can claim a due time; an insert error is returned as insertErr and the claim still commits.
func (s *SchedulerService) claimAndCreateRun(ctx context.Context, analysis ScheduledAnalysis, now, nextRun time.Time) (runID string, claimed bool, insertErr error, err error) {
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().
			Model((*ScheduledAnalysis)(nil)).
			Set("last_scheduled_run = ?", now).
			Set("next_scheduled_run = ?", nextRun).
			Where("id = ?", analysis.ID).
			Where("is_active = ?", true).
			Where("schedule_type IN (?)", bun.List(recurringTypes)).
			Where("next_scheduled_run <= ?", now).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim schedule: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		claimed = true

		// A failed insert rolls back to here, keeping the claim.
		if _, err := tx.ExecContext(ctx, "SAVEPOINT new_run"); err != nil {
			return fmt.Errorf("savepoint: %w", err)
		}
		run := newRun(analysis, now)
		if _, insertErr = tx.NewInsert().Model(&run).Returning("id").Exec(ctx); insertErr != nil {
			_, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT new_run")
			return err
		}
		runID = run.ID
		return nil
	})
	if err != nil {
		return "", false, nil, err
	}
	return runID, claimed, insertErr, nil
}

// newRun copies a scheduled analysis into a new one-time run.
func newRun(analysis ScheduledAnalysis, now time.Time) ScheduledAnalysis {
	once := "once"
	return ScheduledAnalysis{
		CreatedOn:      now,
		Config:         analysis.Config,
		Stage:          0,
		Status:         string(codeclarity.REQUESTED),
		Steps:          analysis.Steps,
		Branch:         analysis.Branch,
		Tag:            analysis.Tag,
		CommitHash:     analysis.CommitHash,
		ScheduleType:   &once,
		IsActive:       true,
		ProjectID:      analysis.ProjectID,
		AnalyzerID:     analysis.AnalyzerID,
		OrganizationID: analysis.OrganizationID,
		IntegrationID:  analysis.IntegrationID,
		CreatedByID:    analysis.CreatedByID,
	}
}

// failRun marks a run nobody will process as failed, so it is not left requested.
func (s *SchedulerService) failRun(ctx context.Context, log *logrus.Entry, runID string) {
	_, err := s.db.NewUpdate().
		Model((*ScheduledAnalysis)(nil)).
		Set("status = ?", string(codeclarity.FAILURE)).
		Set("failure_reason = ?", "The scheduled run could not be queued.").
		Set("ended_on = ?", s.now()).
		Where("id = ?", runID).
		Where("status = ?", string(codeclarity.REQUESTED)).
		Exec(ctx)
	if err != nil {
		log.WithError(err).Error("Failed to mark unqueued scheduled run as failed")
	}
}

func (s *SchedulerService) sendAnalysisMessage(analysis ScheduledAnalysis, runID string) error {
	// Create message in the format expected by dispatcher
	message := map[string]any{
		"analysis_id":     runID,
		"project_id":      analysis.ProjectID,
		"integration_id":  analysis.IntegrationID,
		"organization_id": analysis.OrganizationID,
		"config":          analysis.Config,
	}

	body, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return s.publish("api_request", body)
}

func calculateNextRun(scheduleType *string, from time.Time) time.Time {
	if scheduleType != nil && *scheduleType == "weekly" {
		return from.Add(7 * 24 * time.Hour)
	}
	return from.Add(24 * time.Hour)
}

func main() {
	service, err := CreateSchedulerService()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create scheduler service")
	}
	defer func() { _ = service.Close() }()

	service.Start()
}
