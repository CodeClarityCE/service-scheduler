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
- **API Integration**: Creates new analysis executions via REST API
- **Automatic Rescheduling**: Calculates and sets next run times after execution

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

### API Connection
- `API_BASE_URL`: Base URL for the CodeClarity API (default: http://localhost:3000)

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
- **API**: Creates new analysis executions and reads scheduled analyses from the database
- **Dispatcher**: Sends analysis start messages via RabbitMQ
- **Frontend**: Users can create scheduled analyses through the web interface

### How It Works

1. **Startup**: The service starts a cron scheduler that runs every minute
2. **Analysis Discovery**: Queries the database for analyses with:
   - `is_active = true`
   - `schedule_type` in ['daily', 'weekly']
   - `next_scheduled_run <= now()`
3. **Execution Creation**: Calls the API to create a new analysis execution (preserving historical results)
4. **Message Dispatch**: Sends analysis start messages to RabbitMQ with the new execution ID
5. **Schedule Update**: Updates `last_scheduled_run` and calculates `next_scheduled_run`

## Contributing

If you'd like to contribute code or documentation, please see [CONTRIBUTING.md](https://github.com/CodeClarityCE/codeclarity-dev/blob/main/CONTRIBUTING.md) for guidelines on how to do so.

## Reporting Issues

Please report any issues with the setup process or other problems encountered while using this repository by opening a new issue in this project's GitHub page.