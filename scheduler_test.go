package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CodeClarityCE/utility-boilerplates/testdeps"
	"github.com/uptrace/bun"
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
	if service.db == nil || service.publish == nil || service.now == nil {
		t.Error("Expected database, publisher and clock to be set")
	}
}

// The analysis table as the API's codeclarity baseline migration creates it.
var analysisSchema = []string{
	`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
	`CREATE TYPE "analysis_schedule_type_enum" AS ENUM ('once', 'daily', 'weekly')`,
	`CREATE TABLE "analysis" (
  "id" uuid DEFAULT uuid_generate_v4() NOT NULL PRIMARY KEY,
  "created_on" timestamp with time zone NOT NULL,
  "config" jsonb NOT NULL,
  "stage" integer,
  "status" character varying NOT NULL,
  "steps" jsonb NOT NULL,
  "started_on" timestamp with time zone,
  "ended_on" timestamp with time zone,
  "branch" character varying(25) NOT NULL,
  "tag" character varying(25),
  "commit_hash" character varying(64),
  "projectId" uuid,
  "analyzerId" uuid,
  "organizationId" uuid,
  "integrationId" uuid,
  "createdById" uuid,
  "schedule_type" analysis_schedule_type_enum DEFAULT 'once'::analysis_schedule_type_enum,
  "next_scheduled_run" timestamp with time zone,
  "is_active" boolean DEFAULT true NOT NULL,
  "last_scheduled_run" timestamp with time zone,
  "failure_reason" character varying(500),
  "vulnerability_instances_stored_on" timestamp with time zone,
  "vulnerability_instances_have_results" boolean DEFAULT false NOT NULL
)`,
}

const (
	scheduledID   = "11111111-1111-1111-1111-111111111111"
	projectID     = "22222222-2222-2222-2222-222222222222"
	analyzerID    = "33333333-3333-3333-3333-333333333333"
	orgID         = "44444444-4444-4444-4444-444444444444"
	integrationID = "55555555-5555-5555-5555-555555555555"
	userID        = "66666666-6666-6666-6666-666666666666"
)

type published struct {
	queue string
	body  map[string]any
}

type outbox struct {
	mu   sync.Mutex
	msgs []published
}

func (o *outbox) sent() []published {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]published(nil), o.msgs...)
}

var now = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

// testScheduler returns a scheduler on a fresh analysis table whose publisher records
// messages, or fails them when publishErr is set.
func testScheduler(t *testing.T, publishErr error) (*SchedulerService, *bun.DB, *outbox) {
	t.Helper()
	db := testdeps.Postgres(t)
	for _, stmt := range analysisSchema {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("prepare analysis schema: %v", err)
		}
	}
	out := &outbox{}
	s := &SchedulerService{
		db:  db,
		now: func() time.Time { return now },
		publish: func(queue string, body []byte) error {
			if publishErr != nil {
				return publishErr
			}
			var msg map[string]any
			if err := json.Unmarshal(body, &msg); err != nil {
				return err
			}
			out.mu.Lock()
			defer out.mu.Unlock()
			out.msgs = append(out.msgs, published{queue, msg})
			return nil
		},
	}
	return s, db, out
}

// insertScheduled adds a scheduled analysis due one minute ago.
func insertScheduled(t *testing.T, db *bun.DB, scheduleType string) {
	t.Helper()
	due := now.Add(-time.Minute)
	_, err := db.ExecContext(context.Background(), `INSERT INTO analysis
		(id, created_on, config, stage, status, steps, branch, tag, commit_hash,
		 "projectId", "analyzerId", "organizationId", "integrationId", "createdById",
		 schedule_type, next_scheduled_run, is_active, started_on)
		VALUES (?, ?, '{"js-sbom":{"path":"/"}}', 3, 'completed', '[[{"Name":"js-sbom","Status":"success"}]]',
		 'main', 'v1.0', 'abc123', ?, ?, ?, ?, ?, ?, ?, true, ?)`,
		scheduledID, due.Add(-48*time.Hour), projectID, analyzerID, orgID, integrationID, userID,
		scheduleType, due, due.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("insert scheduled analysis: %v", err)
	}
}

type runRow struct {
	ID               string     `bun:"id"`
	Status           string     `bun:"status"`
	Stage            *int       `bun:"stage"`
	Config           string     `bun:"config"`
	Steps            string     `bun:"steps"`
	Branch           string     `bun:"branch"`
	Tag              *string    `bun:"tag"`
	CommitHash       *string    `bun:"commit_hash"`
	ProjectID        *string    `bun:"projectId"`
	AnalyzerID       *string    `bun:"analyzerId"`
	OrganizationID   *string    `bun:"organizationId"`
	IntegrationID    *string    `bun:"integrationId"`
	CreatedByID      *string    `bun:"createdById"`
	ScheduleType     *string    `bun:"schedule_type"`
	IsActive         bool       `bun:"is_active"`
	NextScheduledRun *time.Time `bun:"next_scheduled_run"`
	LastScheduledRun *time.Time `bun:"last_scheduled_run"`
	StartedOn        *time.Time `bun:"started_on"`
	CreatedOn        time.Time  `bun:"created_on"`
	FailureReason    *string    `bun:"failure_reason"`
}

const runColumns = `id, status, stage, config::text AS config, steps::text AS steps, branch, tag, commit_hash,
	"projectId", "analyzerId", "organizationId", "integrationId", "createdById",
	schedule_type::text AS schedule_type, is_active, next_scheduled_run, last_scheduled_run,
	started_on, created_on, failure_reason`

func runs(t *testing.T, db *bun.DB) []runRow {
	t.Helper()
	var rows []runRow
	err := db.NewRaw("SELECT "+runColumns+" FROM analysis WHERE id <> ?", scheduledID).
		Scan(context.Background(), &rows)
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	return rows
}

func scheduled(t *testing.T, db *bun.DB) runRow {
	t.Helper()
	var row runRow
	err := db.NewRaw("SELECT "+runColumns+" FROM analysis WHERE id = ?", scheduledID).
		Scan(context.Background(), &row)
	if err != nil {
		t.Fatalf("read scheduled analysis: %v", err)
	}
	return row
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func wantNextRun(t *testing.T, db *bun.DB, after time.Duration) {
	t.Helper()
	sched := scheduled(t, db)
	if sched.NextScheduledRun == nil || !sched.NextScheduledRun.Equal(now.Add(after)) {
		t.Errorf("want next run %v, got %v", now.Add(after), sched.NextScheduledRun)
	}
	if sched.LastScheduledRun == nil || !sched.LastScheduledRun.Equal(now) {
		t.Errorf("want last run %v, got %v", now, sched.LastScheduledRun)
	}
}

func TestDueDailyAnalysisCreatesOneRequestedRun(t *testing.T) {
	s, db, out := testScheduler(t, nil)
	insertScheduled(t, db, "daily")

	s.processDueAnalyses(context.Background())

	got := runs(t, db)
	if len(got) != 1 {
		t.Fatalf("want 1 new run, got %d", len(got))
	}
	run := got[0]
	if run.Status != "requested" || run.Stage == nil || *run.Stage != 0 {
		t.Errorf("want status requested and stage 0, got %q and %v", run.Status, run.Stage)
	}
	if run.Config != `{"js-sbom": {"path": "/"}}` || run.Steps != `[[{"Name": "js-sbom", "Status": "success"}]]` {
		t.Errorf("want config and steps copied, got %s and %s", run.Config, run.Steps)
	}
	if run.Branch != "main" || str(run.Tag) != "v1.0" || str(run.CommitHash) != "abc123" {
		t.Errorf("want branch, tag and commit copied, got %q %q %q", run.Branch, str(run.Tag), str(run.CommitHash))
	}
	for name, pair := range map[string][2]string{
		"project":      {str(run.ProjectID), projectID},
		"analyzer":     {str(run.AnalyzerID), analyzerID},
		"organization": {str(run.OrganizationID), orgID},
		"integration":  {str(run.IntegrationID), integrationID},
		"created by":   {str(run.CreatedByID), userID},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: want %s, got %s", name, pair[1], pair[0])
		}
	}
	if str(run.ScheduleType) != "once" || !run.IsActive || run.NextScheduledRun != nil || run.LastScheduledRun != nil {
		t.Errorf("want a once, active run without schedule times, got %q active=%v next=%v last=%v",
			str(run.ScheduleType), run.IsActive, run.NextScheduledRun, run.LastScheduledRun)
	}
	if run.StartedOn != nil || !run.CreatedOn.Equal(now) {
		t.Errorf("want created now and not started, got created %v started %v", run.CreatedOn, run.StartedOn)
	}

	wantNextRun(t, db, 24*time.Hour)

	sent := out.sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sent))
	}
	msg := sent[0]
	if msg.queue != "api_request" || msg.body["analysis_id"] != run.ID || msg.body["project_id"] != projectID ||
		msg.body["integration_id"] != integrationID || msg.body["organization_id"] != orgID {
		t.Errorf("unexpected message %s %v", msg.queue, msg.body)
	}
	if cfg, ok := msg.body["config"].(map[string]any); !ok || cfg["js-sbom"] == nil {
		t.Errorf("want the config in the message, got %v", msg.body["config"])
	}
}

func TestDueWeeklyAnalysisMovesSevenDays(t *testing.T) {
	s, db, _ := testScheduler(t, nil)
	insertScheduled(t, db, "weekly")

	s.processDueAnalyses(context.Background())

	wantNextRun(t, db, 7*24*time.Hour)
	if n := len(runs(t, db)); n != 1 {
		t.Errorf("want 1 run, got %d", n)
	}
}

func TestPublishFailureStillAdvancesAndLeavesNoRequestedRun(t *testing.T) {
	s, db, _ := testScheduler(t, errors.New("broker down"))
	insertScheduled(t, db, "daily")

	s.processDueAnalyses(context.Background())
	s.processDueAnalyses(context.Background())

	got := runs(t, db)
	if len(got) != 1 {
		t.Fatalf("want the one failed run, got %d runs", len(got))
	}
	if got[0].Status != "failure" || str(got[0].FailureReason) == "<nil>" {
		t.Errorf("want the run marked failure with a reason, got %q %q", got[0].Status, str(got[0].FailureReason))
	}
	wantNextRun(t, db, 24*time.Hour)
}

func TestInsertFailureStillAdvancesAndSecondTickCreatesNoRun(t *testing.T) {
	s, db, out := testScheduler(t, nil)
	insertScheduled(t, db, "daily")
	// Refuse every new once run, so the insert fails.
	if _, err := db.ExecContext(context.Background(),
		`ALTER TABLE analysis ADD CONSTRAINT no_runs CHECK (schedule_type <> 'once')`); err != nil {
		t.Fatalf("add constraint: %v", err)
	}

	s.processDueAnalyses(context.Background())
	s.processDueAnalyses(context.Background())

	if n := len(runs(t, db)); n != 0 {
		t.Errorf("want no runs, got %d", n)
	}
	if n := len(out.sent()); n != 0 {
		t.Errorf("want no message, got %d", n)
	}
	wantNextRun(t, db, 24*time.Hour)
}

func TestTwoTicksForOneDueTimeCreateOneRun(t *testing.T) {
	s, db, out := testScheduler(t, nil)
	insertScheduled(t, db, "daily")

	// Both ticks read the due analysis before either claims it.
	due, err := s.dueAnalyses(context.Background())
	if err != nil || len(due) != 1 {
		t.Fatalf("want 1 due analysis, got %d (%v)", len(due), err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { s.processAnalysis(context.Background(), due[0]) })
	}
	wg.Wait()

	if n := len(runs(t, db)); n != 1 {
		t.Errorf("want 1 run, got %d", n)
	}
	if n := len(out.sent()); n != 1 {
		t.Errorf("want 1 message, got %d", n)
	}
}
