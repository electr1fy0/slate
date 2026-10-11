# Current Continuum architecture

The supported design is a single-Region AWS demo with an API Gateway router, two application instances in separate Availability Zones, a regional DynamoDB control record, and a monitored Step Functions recovery workflow. Local mode runs two HTTP servers and shared in-memory state inside one Go process.

Use the [current demo runbook](demo-runbook.md) for the architecture, boundaries, setup, and pause/resume commands. The previous multi-Region Route 53 design is retained as [historical architecture](archive/architecture.md) and is not deployable for this demo.
