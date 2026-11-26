<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://github.com/CodeClarityCE/identity/blob/main/logo/vectorized/logo_name_white.svg">
  <source media="(prefers-color-scheme: light)" srcset="https://github.com/CodeClarityCE/identity/blob/main/logo/vectorized/logo_name_black.svg">
  <img alt="codeclarity-logo" src="https://github.com/CodeClarityCE/identity/blob/main/logo/vectorized/logo_name_black.svg">
</picture>
<br>
<br>

Secure your software empower your team.

[![License](https://img.shields.io/github/license/codeclarityce/codeclarity-dev)](LICENSE.txt)

<details open="open">
<summary>Table of Contents</summary>

- [CodeClarity Service - Scheduler](#codeclarity-service---scheduler)
  - [Overview](#overview)
  - [Features](#features)
  - [Configuration](#configuration)
  - [Usage](#usage)
  - [Integration](#integration)
  - [Contributing](#contributing)
  - [Reporting Issues](#reporting-issues)

</details>

---

# CodeClarity Service - Scheduler

## Overview

The scheduler service is responsible for managing and executing scheduled analyses in the CodeClarity platform. It monitors scheduled analyses and triggers their execution at the appropriate times while preserving historical results.

## Features

- **Recurring Analysis Execution**: Supports daily and weekly schedules
- **Historical Result Preservation**: Each scheduled execution creates a new analysis record
- **Cron-based Scheduling**: Uses robust cron scheduling with second-level precision
- **Database Integration**: Monitors the analysis table for due scheduled analyses
- **RabbitMQ Integration**: Sends analysis start messages to the dispatcher queue
- **Direct Run Creation**: Inserts each new run into the `codeclarity` database; it makes no API calls
- **Automatic Rescheduling**: Moves the next run time forward at every due time, even when the run fails

## Configuration

The service uses environment variables for configuration:

### Database Connection

- `PG_DB_HOST`: PostgreSQL host (default: localhost)
- `PG_DB_PORT`: PostgreSQL port (default: 6432)
- `PG_DB_USER`: PostgreSQL username (default: postgres)
- `PG_DB_PASSWORD`: PostgreSQL password (default: password)
- `PG_DB_NAME`: Database name (default: codeclarity)

### RabbitMQ Connection

- `AMQP_PROTOCOL`: AMQP protocol (default: amqp)
- `AMQP_HOST`: RabbitMQ host (default: localhost)
- `AMQP_PORT`: RabbitMQ port (default: 5672)
- `AMQP_USER`: RabbitMQ username (default: guest)
- `AMQP_PASSWORD`: RabbitMQ password (default: guest)
- `AMQP_ANALYSES_QUEUE`: Queue name for analysis messages (default: api_request)

## Usage

### Build and Run

```bash
# Build the service
make build

# Run the service
make run

# Clean build artifacts
make clean

# Run tests
make test
```

### Docker Integration

The service is designed to run as part of the CodeClarity Docker Compose stack.

## Integration

The scheduler integrates with:

- **Database**: Reads scheduled analyses and inserts their runs in the `codeclarity` database (schema owned by the API migrations)
- **Dispatcher**: Sends analysis start messages via RabbitMQ
- **Frontend**: Users can create scheduled analyses through the web interface

### How It Works

1. **Startup**: The service starts a cron scheduler that runs every minute
2. **Analysis Discovery**: Queries the database for analyses with:
   - `is_active = true`
   - `schedule_type` in ['daily', 'weekly']
   - `next_scheduled_run <= now()`
3. **Claim and Run Creation**: In one transaction, moves `last_scheduled_run` and `next_scheduled_run` forward (so one due time gives one run) and inserts a new `requested` run copied from the scheduled analysis: its config, steps, branch, tag and commit, its project, analyzer, organization, integration and creator links, and its vulnerability policy links in `policy_analyses_analysis`. If the policy links cannot be written, the run is not created either
4. **Message Dispatch**: Sends the `api_request` message with the new run's ID; if that fails, the run is marked `failure`
5. **Failures**: A failed insert or publish is logged and not retried until the next due time

The copy lives in `newRun` and `claimAndCreateRun`. Because the scheduler writes the `analysis` table directly, a new `Analysis` column or relation in the API must be copied here too.

See ADR 0022 in `codeclarity-dev` (`docs/adr/0022-the-scheduler-creates-scheduled-analyses-in-the-database.md`).

## Contributing

If you'd like to contribute code or documentation, please see [CONTRIBUTING.md](https://github.com/CodeClarityCE/codeclarity-dev/blob/main/CONTRIBUTING.md) for guidelines on how to do so.

## Reporting Issues

Please report any issues with the setup process or other problems encountered while using this repository by opening a new issue in this project's GitHub page.
