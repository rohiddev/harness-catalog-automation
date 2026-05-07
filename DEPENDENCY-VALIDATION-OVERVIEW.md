# Service Dependency Validation — Overview

**Audience:** Head of Cloud  
**Purpose:** Explain how we validate service dependencies automatically and keep blast radius data accurate

---

## The Problem Today

When a critical service goes down, two questions need immediate answers:

1. **What else breaks?** (blast radius)
2. **Who owns those services?** (notification routing)

Today, the answer comes from the CMDB — which is only as accurate as what teams manually declared. Teams forget to update it. Dependencies change. The CMDB drifts.

**Result:** blast radius reports are incomplete, incident response is slower than it should be, and auditors cannot fully trust the dependency map.

---

## The Approach — Three Signals, One Answer

Rather than relying on a single source, we cross-reference three independent signals. When all three agree, we have high confidence. When they disagree, we have an automated finding.

```
┌─────────────────────────────────────────────────────────────────┐
│                     THREE SIGNALS                               │
│                                                                 │
│  1. SBOM              2. Observability        3. CMDB           │
│  (Code tells us)      (Production tells us)   (Teams declared)  │
│                                                                 │
│  What libraries       What services actually   What teams said  │
│  are in the code      call each other          they depend on   │
│  e.g. kafka-clients   e.g. loan-app → person   e.g. now.yaml   │
│                                                                 │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
              Cross-reference engine
                         │
              ┌──────────┴──────────┐
              │                     │
         All agree            Discrepancy found
              │                     │
         High confidence       Automated finding
         Blast radius          → scorecard fails
         is accurate           → team notified
                               → must resolve
                                 before prod deploy
```

---

## Signal 1 — SBOM (Software Bill of Materials)

**What it is:** An automated scan of every package and library in a service's codebase, generated on every pull request by GitHub Actions.

**What it detects:**

| Technology | Example package signatures |
|---|---|
| Kafka | kafka-clients, spring-kafka, confluent-kafka-python |
| Message Queue | amqp, rabbitmq-client, ibm-mq-allclient |
| Database | pg, mysql-connector-java, pymongo, hibernate |
| FTP | commons-net, ftplib, jsch |
| API | okhttp, axios, requests, httpclient |

**Output:** A dependency flag file (`sbom-deps.json`) committed per repo:
```json
{ "kafka": true, "database": true, "mq": false, "ftp": false, "api": true }
```

**Limitation:** Detects libraries in code — does not see sidecar connections or services without a package manager.

---

## Signal 2 — Observability Platform (Datadog or Dynatrace)

**What it is:** Your APM tool already observes every service call happening in production. We query its topology API nightly to extract what actually depends on what.

**Datadog:** queries the APM service dependency API using the service name from the catalog.  
**Dynatrace:** queries the SmartScape topology API using the entity ID from the catalog.

**What it detects:**
- Service A is calling Service B in production (observed from real traffic)
- Service A is connecting to Database X (observed from DB monitoring)
- Service A has not called Service C in 90 days (stale declared dependency)

**Why this matters:** This is ground truth. It does not depend on what teams said. It reflects what is actually running.

**Limitation:** Only covers services instrumented with APM. Legacy batch jobs or EC2 workloads without an agent may not appear.

---

## Signal 3 — CMDB (Declared Dependencies)

**What it is:** The ServiceNow CMDB (`cmdb_rel_ci`) holds the relationships teams have declared — what depends on what. This feeds the current blast radius reports.

**Today:** Teams declare manually (low accuracy, high drift).  
**With now.yaml:** Teams declare via a structured file in their repo, validated in CI (higher accuracy).  
**With SBOM + Observability:** now.yaml is auto-generated from the other two signals (highest accuracy, least team effort).

---

## How the Cross-Reference Works

A nightly pipeline runs for every Tier-1 and Tier-2 service in the IDP catalog:

```
Step 1: Read SBOM flags from catalog annotation (sbom/kafka, sbom/database, etc.)
Step 2: Query Datadog or Dynatrace API for observed dependencies
Step 3: Query ServiceNow cmdb_rel_ci for declared dependencies
Step 4: Compare all three

Finding A — In Observability but NOT in CMDB:
  "loan-app is calling person-app in production but CMDB has no relationship"
  → Blast radius is INCOMPLETE → scorecard fails

Finding B — In CMDB but NOT in Observability (90+ days):
  "CMDB says loan-app depends on legacy-auth-service but no traffic observed"
  → Stale declaration → team must confirm or remove

Finding C — In SBOM but NOT in CMDB and NOT in Observability:
  "kafka-clients in code but service never seen producing/consuming"
  → Possible future dependency or false positive → team reviews
```

---

## What Teams See

**In the IDP catalog — on the service page:**

```
Blast Radius tab
─────────────────────────────────────────────────────
Last validated:          2026-05-07
Confidence:              High (all 3 signals agree)

Undeclared dependencies: person-app, payments-gateway  ← action required
Stale declarations:      legacy-auth-service            ← review required
SBOM-only findings:      (none)
```

**In the weekly blast radius report (Monday morning):**
- Same findings surfaced to leaders and service owners
- At-risk services ranked by blast radius + signal confidence
- Services with discrepancies flagged as "unverified blast radius"

---

## Production Gate

Services cannot deploy to production until all three signals agree:

```
Scorecard check: blast-radius/validated = true
  → Passes when: CMDB matches Observability matches SBOM
  → Fails when: undeclared or stale dependencies found
  → Result: prod deploy blocked until team resolves
```

This makes dependency accuracy a hard requirement, not a courtesy.

---

## Why now.yaml Can Wait

The now.yaml schema from RapDev is not yet confirmed. This approach does not require it.

SBOM and Observability provide the dependency data immediately. When the now.yaml schema is confirmed, we run a one-time batch to generate now.yaml for every service from the signals already in the catalog — no team effort required at that point.

---

## What We Need to Build

| Component | Effort | Owner |
|---|---|---|
| SBOM generation in GitHub Actions | 1 day | Platform Engineering |
| SBOM → catalog annotation pipeline | 1 day | Platform Engineering |
| Datadog / Dynatrace topology pull (nightly pipeline) | 2-3 days | Platform Engineering |
| Cross-reference and findings pipeline | 2-3 days | Platform Engineering |
| Scorecard check wiring | 1 day | Platform Engineering |
| now.yaml batch generator (when schema confirmed) | 1 day | Platform Engineering |

**Total: approximately 2 weeks. No dependency on RapDev or now.yaml schema.**

---

## Executive Summary

> We validate service dependencies using three independent automated signals — code analysis (SBOM), production traffic (Datadog/Dynatrace), and declared relationships (CMDB). When all three agree, blast radius data is trustworthy. When they disagree, teams are alerted and blocked from deploying to production until resolved. This requires no manual effort from service teams beyond what they already do, and does not depend on the now.yaml schema being finalised.
