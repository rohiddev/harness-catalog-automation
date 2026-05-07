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

**What it is:** Your APM tool already observes every service call happening in production. We query its topology API nightly to extract what actually depends on what. The pipeline works with whichever tool a team has registered in their catalog — Datadog, Dynatrace, or both during a migration period.

**What it detects:**
- Service A is calling Service B in production (observed from real traffic)
- Service A is connecting to Database X (observed from DB monitoring)
- Service A has not called Service C in 90 days (stale declared dependency)

**Why this matters:** This is ground truth. It does not depend on what teams said. It reflects what is actually running.

**Limitation:** Only covers services instrumented with APM. Legacy batch jobs or EC2 workloads without an agent may not appear.

---

### Full Graph Traversal — Tier-1 and Tier-2 Services

For critical services, the pipeline performs full transitive graph traversal — not just direct dependencies — to produce a complete blast radius picture. The key principle is **pull the full graph once, traverse locally** to avoid rate limit issues.

#### Dynatrace — Bulk Relationship Pull

Dynatrace returns all service relationships in one paginated API call. The pipeline loads the full graph into memory and runs BFS locally — no additional API calls per hop.

```
GET /api/v2/entities
    ?entitySelector=type("SERVICE") AND tag("tier:1","tier:2")
    &fields=fromRelationships,toRelationships
    &pageSize=500
```

For a faster result per service, Dynatrace's Davis AI pre-computes impact chains:

```
GET /api/v2/entities?entitySelector=impacted("SERVICE-abc123")
```

Returns every entity Dynatrace already knows is affected — essentially a pre-computed blast radius in one call. No traversal code needed.

**API calls for full Tier-1/2 graph:** 1–3 calls total regardless of graph depth.

#### Datadog — Per-Service Pull with Local Traversal

Datadog does not have a single bulk topology call. The pipeline pulls dependencies per Tier-1/2 service, builds an adjacency list in memory, then traverses locally.

```
GET /api/v1/service_dependencies
    ?service=loan-app
    &env=production
    &start=<7_days_ago>
    &end=<now>
```

Repeated for each Tier-1/2 service from the IDP catalog. The pipeline then runs BFS on the in-memory graph — no further API calls per traversal hop.

**API calls for 30 Tier-1/2 services:** ~30 calls — well within Datadog rate limits.

#### Pipeline Logic — Which Tool to Use

The pipeline checks which annotation is present in the catalog and routes accordingly:

```
if catalog has datadog.com/service-name  → call Datadog APM API
if catalog has dynatrace.com/entity-id   → call Dynatrace SmartScape API
if both present                          → call both, compare findings
if neither present                       → SBOM + CMDB only (flag missing observability)
```

#### Comparison for Tier-1 Traversal

| | Dynatrace | Datadog |
|---|---|---|
| Full graph retrieval | 1–3 API calls (bulk) | 1 call per Tier-1 service |
| Pre-computed blast radius | Yes — `impacted()` query | No |
| Rate limit risk | Very low | Low (scoped to Tier-1/2) |
| DB connection discovery | Auto (OneAgent) | Requires Database Monitoring add-on |
| Services without agent | Not visible | Not visible |

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
