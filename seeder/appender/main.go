package main

// seeder/appender/main.go
// Reads pipeline variables from env and updates catalog-info.yaml:
//   - Updates Component annotations (observability, on-call, db, kubernetes)
//   - Updates System metadata when UPDATE_SYSTEM=Yes
//   - Appends/updates Resource entities (databases, caches, kafka, mq, ftp)
//   - Appends/updates API entities (rest, kafka producers/consumers, mq, ftp)
//   - Syncs Component spec.dependsOn / spec.providesApis / spec.consumesApis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Entity struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Identifier string                 `yaml:"identifier"`
	Name       string                 `yaml:"name"`
	Type       string                 `yaml:"type,omitempty"`
	Owner      string                 `yaml:"owner"`
	Metadata   EntityMetadata         `yaml:"metadata"`
	Spec       map[string]interface{} `yaml:"spec,omitempty"`
}

type EntityMetadata struct {
	Title       string            `yaml:"title,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"`
	Links       []EntityLink      `yaml:"links,omitempty"`
}

type EntityLink struct {
	URL   string `yaml:"url"`
	Title string `yaml:"title"`
	Icon  string `yaml:"icon,omitempty"`
}

// JSON input types from workflow parameters

type ResourceItem struct {
	Name        string `json:"name"`
	Engine      string `json:"engine,omitempty"`
	Description string `json:"description"`
}

type RestAPIItem struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
}

type KafkaItem struct {
	Name          string `json:"name"`
	Topic         string `json:"topic"`
	ConsumerGroup string `json:"consumerGroup,omitempty"`
	Description   string `json:"description"`
}

type MQItem struct {
	Name        string `json:"name"`
	QueueName   string `json:"queueName"`
	Description string `json:"description"`
}

type FTPAPIItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Description string `json:"description"`
}

type KubernetesWorkload struct {
	Cluster       string `json:"cluster"`
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
	Environment   string `json:"environment"`
}

type KafkaClusterItem struct {
	Name             string `json:"name"`
	BootstrapServers string `json:"bootstrapServers,omitempty"`
	Description      string `json:"description"`
}

type MQBrokerItem struct {
	Name         string `json:"name"`
	Host         string `json:"host,omitempty"`
	QueueManager string `json:"queueManager,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Description  string `json:"description"`
}

func main() {
	catalogFile := env("CATALOG_FILE", "catalog-info.yaml")

	entities, err := readEntities(catalogFile)
	if err != nil {
		log.Fatalf("read %s: %v", catalogFile, err)
	}

	comp := findByKind(entities, "Component")
	if comp == nil {
		log.Fatalf("no Component entity found in %s", catalogFile)
	}

	// Extract stable values from existing Component annotations (set at seed time)
	sysid := env("SYSID", "")
	tier := annotationOf(comp, "bank.com/tier")
	ownerEmail := annotationOf(comp, "bank.com/owner-email")
	ghSlug := annotationOf(comp, "github.com/project-slug")
	systemIdentifier := env("SYSTEM_IDENTIFIER", "")

	updateComponent(comp)

	if env("UPDATE_SYSTEM", "") == "Yes" {
		if sys := findByKind(entities, "System"); sys != nil {
			updateSystem(sys)
		}
	}

	newEntities := generateEntities(comp.Owner, sysid, tier, ownerEmail, ghSlug, systemIdentifier)
	entities = mergeEntities(entities, newEntities)
	syncComponentSpec(comp, newEntities)

	if err := writeEntities(catalogFile, entities); err != nil {
		log.Fatalf("write %s: %v", catalogFile, err)
	}
	fmt.Printf("appender: %d entities written to %s (%d appended/updated)\n", len(entities), catalogFile, len(newEntities))
}

// ─── Entity I/O ───────────────────────────────────────────────────────────

func readEntities(path string) ([]*Entity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entities []*Entity
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var e Entity
		if err := dec.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if e.APIVersion != "" {
			entities = append(entities, &e)
		}
	}
	return entities, nil
}

func writeEntities(path string, entities []*Entity) error {
	var buf bytes.Buffer
	for i, e := range entities {
		if i > 0 {
			buf.WriteString("---\n\n")
		}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(e); err != nil {
			return err
		}
		enc.Close()
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// ─── Component update ─────────────────────────────────────────────────────

func updateComponent(e *Entity) {
	if e.Metadata.Annotations == nil {
		e.Metadata.Annotations = make(map[string]string)
	}
	if v := env("COMPONENT_DESCRIPTION", ""); v != "" {
		e.Metadata.Description = v
	}
	setAnno(e, "datadoghq.com/dashboard-url", env("DATADOG_DASHBOARD_URL", ""))
	setAnno(e, "datadoghq.com/service-name", env("DATADOG_SERVICE_NAME", ""))
	setAnno(e, "dynatrace.com/dashboard-url", env("DYNATRACE_DASHBOARD_URL", ""))
	setAnno(e, "dynatrace.com/entity-id", env("DYNATRACE_ENTITY_ID", ""))
	setAnno(e, "wiz.io/project-id", env("WIZ_PROJECT_ID", ""))
	setAnno(e, "sonarqube.org/project-key", env("SONARQUBE_PROJECT_KEY", ""))
	setAnno(e, "sonarqube.org/dashboard-url", env("SONARQUBE_DASHBOARD_URL", ""))
	setAnno(e, "quality/coverage-target", env("QUALITY_COVERAGE_TARGET", ""))
	setAnno(e, "quality/dora-dashboard-url", env("QUALITY_DORA_DASHBOARD_URL", ""))
	setAnno(e, "oncall/team", env("ONCALL_TEAM", ""))
	setAnno(e, "oncall/rotation-url", env("ONCALL_ROTATION_URL", ""))
	setAnno(e, "oncall/incidents-url", env("ONCALL_INCIDENTS_URL", ""))
	setAnno(e, "oncall/last-incident-date", env("ONCALL_LAST_INCIDENT_DATE", ""))
	setAnno(e, "db/engine", env("DB_ENGINE", ""))
	setAnno(e, "db/schema", env("DB_SCHEMA", ""))
	setAnno(e, "db/dba-runbook-url", env("DB_DBA_RUNBOOK_URL", ""))
	setAnno(e, "db/last-migration", env("DB_LAST_MIGRATION", ""))
	setAnno(e, "db/backup-policy", env("DB_BACKUP_POLICY", ""))
	applyK8sAnnotations(e)

	upsertLink(e, "Confluence", env("COMPONENT_CONFLUENCE_URL", ""), "docs")
	upsertLink(e, "Runbook", env("COMPONENT_RUNBOOK_URL", ""), "book")
	if v := env("DATADOG_DASHBOARD_URL", ""); v != "" {
		upsertLink(e, "Datadog", v, "dashboard")
	}
	if v := env("SONARQUBE_DASHBOARD_URL", ""); v != "" {
		upsertLink(e, "SonarQube", v, "quality")
	}
	if v := env("ONCALL_ROTATION_URL", ""); v != "" {
		upsertLink(e, "On-Call Schedule", v, "group")
	}
}

func applyK8sAnnotations(e *Entity) {
	raw := os.Getenv("KUBERNETES_WORKLOADS")
	if raw == "" || raw == "null" || raw == "[]" {
		return
	}
	var workloads []KubernetesWorkload
	if err := json.Unmarshal([]byte(raw), &workloads); err != nil || len(workloads) == 0 {
		return
	}
	// Pick the entry matching TARGET_ENV; fall back to prod; fall back to first
	targetEnv := env("TARGET_ENV", "prod")
	chosen := workloads[0]
	for _, w := range workloads {
		if w.Environment == targetEnv {
			chosen = w
			break
		}
	}
	if chosen.Environment != targetEnv {
		for _, w := range workloads {
			if w.Environment == "prod" {
				chosen = w
				break
			}
		}
	}
	setAnno(e, "backstage.io/kubernetes-id", e.Identifier)
	setAnno(e, "backstage.io/kubernetes-label-selector", chosen.LabelSelector)
	setAnno(e, "backstage.io/kubernetes-namespace", chosen.Namespace)
}

// ─── System update ────────────────────────────────────────────────────────

func updateSystem(e *Entity) {
	if e.Metadata.Annotations == nil {
		e.Metadata.Annotations = make(map[string]string)
	}
	if v := env("SYSTEM_TITLE", ""); v != "" {
		e.Metadata.Title = v
	}
	if v := env("SYSTEM_DESCRIPTION", ""); v != "" {
		e.Metadata.Description = v
	}
	if v := env("SYSTEM_DOMAIN", ""); v != "" {
		if e.Spec == nil {
			e.Spec = make(map[string]interface{})
		}
		e.Spec["domain"] = v
	}
	setAnno(e, "datadoghq.com/system-dashboard-url", env("SYSTEM_DATADOG_DASHBOARD_URL", ""))
	setAnno(e, "dynatrace.com/system-dashboard-url", env("SYSTEM_DYNATRACE_DASHBOARD_URL", ""))
	setAnno(e, "kubernetes/clusters", env("SYSTEM_KUBERNETES_CLUSTERS", ""))
	setAnno(e, "wiz.io/project-id", env("SYSTEM_WIZ_PROJECT_ID", ""))
	setAnno(e, "oncall/team", env("SYSTEM_ONCALL_TEAM", ""))
	setAnno(e, "oncall/rotation-url", env("SYSTEM_ONCALL_ROTATION_URL", ""))
	setAnno(e, "oncall/incidents-url", env("SYSTEM_ONCALL_INCIDENTS_URL", ""))
	if v := env("SYSTEM_TAGS", ""); v != "" {
		e.Metadata.Tags = splitCSV(v)
	}
	upsertLink(e, "Confluence Space", env("SYSTEM_CONFLUENCE", ""), "docs")
	upsertLink(e, "Platform Runbook", env("SYSTEM_RUNBOOK", ""), "book")
}

// ─── Entity generation ────────────────────────────────────────────────────

func generateEntities(owner, sysid, tier, ownerEmail, ghSlug, systemIdentifier string) []*Entity {
	var out []*Entity

	baseAnnos := map[string]string{
		"bank.com/sysid":          sysid,
		"bank.com/tier":           tier,
		"bank.com/owner-email":    ownerEmail,
		"github.com/project-slug": ghSlug,
	}
	sysRef := []string{"system:account/" + systemIdentifier}

	// Resources
	for _, it := range parseResourceItems(os.Getenv("DATABASES")) {
		e := newResource(it.Name, "database", it.Description, owner, baseAnnos, sysRef)
		if it.Engine != "" {
			e.Metadata.Annotations["db/engine"] = it.Engine
		}
		out = append(out, e)
	}
	for _, it := range parseResourceItems(os.Getenv("CACHES")) {
		e := newResource(it.Name, "cache", it.Description, owner, baseAnnos, sysRef)
		if it.Engine != "" {
			e.Metadata.Annotations["cache/engine"] = it.Engine
		}
		out = append(out, e)
	}
	for _, it := range parseKafkaClusterItems(os.Getenv("KAFKA_CLUSTERS")) {
		e := newResource(it.Name, "kafka", it.Description, owner, baseAnnos, sysRef)
		if it.BootstrapServers != "" {
			e.Metadata.Annotations["kafka/bootstrap-servers"] = it.BootstrapServers
		}
		out = append(out, e)
	}
	for _, it := range parseMQBrokerItems(os.Getenv("MQ_BROKERS")) {
		e := newResource(it.Name, "ibm-mq", it.Description, owner, baseAnnos, sysRef)
		if it.Host != "" {
			e.Metadata.Annotations["mq/host"] = it.Host
		}
		if it.QueueManager != "" {
			e.Metadata.Annotations["mq/queue-manager"] = it.QueueManager
		}
		if it.Channel != "" {
			e.Metadata.Annotations["mq/channel"] = it.Channel
		}
		out = append(out, e)
	}
	for _, it := range parseResourceItems(os.Getenv("FTP_SERVERS")) {
		out = append(out, newResource(it.Name, "file-server", it.Description, owner, baseAnnos, sysRef))
	}

	// APIs
	for _, it := range parseRestAPIItems(os.Getenv("REST_APIS")) {
		e := newAPI(it.Name, "openapi", it.Description, owner, baseAnnos, sysRef)
		if it.Endpoint != "" {
			e.Metadata.Annotations["api/endpoint"] = it.Endpoint
		}
		out = append(out, e)
	}
	for _, it := range parseKafkaItems(os.Getenv("KAFKA_PRODUCERS")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.Topic != "" {
			e.Metadata.Annotations["kafka/topic"] = it.Topic
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "producer")
		out = append(out, e)
	}
	for _, it := range parseKafkaItems(os.Getenv("KAFKA_CONSUMERS")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.Topic != "" {
			e.Metadata.Annotations["kafka/topic"] = it.Topic
		}
		if it.ConsumerGroup != "" {
			e.Metadata.Annotations["kafka/consumer-group"] = it.ConsumerGroup
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "consumer")
		out = append(out, e)
	}
	for _, it := range parseMQItems(os.Getenv("MQ_PRODUCERS")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.QueueName != "" {
			e.Metadata.Annotations["mq/queue"] = it.QueueName
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "producer")
		out = append(out, e)
	}
	for _, it := range parseMQItems(os.Getenv("MQ_CONSUMERS")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.QueueName != "" {
			e.Metadata.Annotations["mq/queue"] = it.QueueName
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "consumer")
		out = append(out, e)
	}
	for _, it := range parseFTPItems(os.Getenv("FTP_OUTBOUND")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.Path != "" {
			e.Metadata.Annotations["ftp/path"] = it.Path
		}
		if it.ContentType != "" {
			e.Metadata.Annotations["ftp/content-type"] = it.ContentType
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "outbound")
		out = append(out, e)
	}
	for _, it := range parseFTPItems(os.Getenv("FTP_INBOUND")) {
		e := newAPI(it.Name, "asyncapi", it.Description, owner, baseAnnos, sysRef)
		if it.Path != "" {
			e.Metadata.Annotations["ftp/path"] = it.Path
		}
		if it.ContentType != "" {
			e.Metadata.Annotations["ftp/content-type"] = it.ContentType
		}
		e.Metadata.Tags = append(e.Metadata.Tags, "inbound")
		out = append(out, e)
	}

	return out
}

func newResource(name, resType, description, owner string, baseAnnos map[string]string, sysRef []string) *Entity {
	return &Entity{
		APIVersion: "harness.io/v1",
		Kind:       "Resource",
		Identifier: toID(name),
		Name:       name,
		Type:       resType,
		Owner:      owner,
		Metadata: EntityMetadata{
			Title:       toTitle(name),
			Description: description,
			Annotations: cloneAnnotations(baseAnnos),
			Tags:        []string{resType},
		},
		Spec: map[string]interface{}{
			"system": sysRef,
		},
	}
}

func newAPI(name, apiType, description, owner string, baseAnnos map[string]string, sysRef []string) *Entity {
	return &Entity{
		APIVersion: "harness.io/v1",
		Kind:       "API",
		Identifier: toID(name),
		Name:       name,
		Type:       apiType,
		Owner:      owner,
		Metadata: EntityMetadata{
			Title:       toTitle(name),
			Description: description,
			Annotations: cloneAnnotations(baseAnnos),
			Tags:        []string{apiType},
		},
		Spec: map[string]interface{}{
			"lifecycle":  "production",
			"system":     sysRef,
			"definition": "# Replace with actual API specification",
		},
	}
}

// ─── Merge + spec sync ────────────────────────────────────────────────────

func mergeEntities(existing, incoming []*Entity) []*Entity {
	for _, ne := range incoming {
		replaced := false
		for i, e := range existing {
			if e.Identifier == ne.Identifier {
				existing[i] = ne
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, ne)
		}
	}
	return existing
}

func syncComponentSpec(comp *Entity, newEntities []*Entity) {
	if comp.Spec == nil {
		comp.Spec = make(map[string]interface{})
	}
	dependsOn := toStrSlice(comp.Spec["dependsOn"])
	providesApis := toStrSlice(comp.Spec["providesApis"])
	consumesApis := toStrSlice(comp.Spec["consumesApis"])

	providesNames := nameSet(
		parseAnyNames(os.Getenv("REST_APIS")),
		parseAnyNames(os.Getenv("KAFKA_PRODUCERS")),
		parseAnyNames(os.Getenv("MQ_PRODUCERS")),
		parseAnyNames(os.Getenv("FTP_OUTBOUND")),
	)
	consumesNames := nameSet(
		parseAnyNames(os.Getenv("KAFKA_CONSUMERS")),
		parseAnyNames(os.Getenv("MQ_CONSUMERS")),
		parseAnyNames(os.Getenv("FTP_INBOUND")),
	)

	for _, ne := range newEntities {
		switch ne.Kind {
		case "Resource":
			ref := "resource:account/" + ne.Identifier
			if !sliceContains(dependsOn, ref) {
				dependsOn = append(dependsOn, ref)
			}
		case "API":
			if providesNames[ne.Name] && !sliceContains(providesApis, ne.Name) {
				providesApis = append(providesApis, ne.Name)
			}
			if consumesNames[ne.Name] && !sliceContains(consumesApis, ne.Name) {
				consumesApis = append(consumesApis, ne.Name)
			}
		}
	}

	if len(dependsOn) > 0 {
		comp.Spec["dependsOn"] = dependsOn
	}
	if len(providesApis) > 0 {
		comp.Spec["providesApis"] = providesApis
	}
	if len(consumesApis) > 0 {
		comp.Spec["consumesApis"] = consumesApis
	}
}

// ─── JSON parse helpers ───────────────────────────────────────────────────

func parseResourceItems(raw string) []ResourceItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []ResourceItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseKafkaClusterItems(raw string) []KafkaClusterItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []KafkaClusterItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseMQBrokerItems(raw string) []MQBrokerItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []MQBrokerItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseRestAPIItems(raw string) []RestAPIItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []RestAPIItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseKafkaItems(raw string) []KafkaItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []KafkaItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseMQItems(raw string) []MQItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []MQItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseFTPItems(raw string) []FTPAPIItem {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var out []FTPAPIItem
	json.Unmarshal([]byte(raw), &out) //nolint:errcheck
	return out
}

func parseAnyNames(raw string) []string {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var items []struct {
		Name string `json:"name"`
	}
	json.Unmarshal([]byte(raw), &items) //nolint:errcheck
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	return names
}

// ─── Small utilities ──────────────────────────────────────────────────────

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func annotationOf(e *Entity, key string) string {
	if e.Metadata.Annotations == nil {
		return ""
	}
	return e.Metadata.Annotations[key]
}

func findByKind(entities []*Entity, kind string) *Entity {
	for _, e := range entities {
		if e.Kind == kind {
			return e
		}
	}
	return nil
}

func setAnno(e *Entity, key, value string) {
	if value == "" {
		return
	}
	if e.Metadata.Annotations == nil {
		e.Metadata.Annotations = make(map[string]string)
	}
	e.Metadata.Annotations[key] = value
}

func upsertLink(e *Entity, title, url, icon string) {
	if url == "" {
		return
	}
	for i, l := range e.Metadata.Links {
		if l.Title == title {
			e.Metadata.Links[i].URL = url
			return
		}
	}
	e.Metadata.Links = append(e.Metadata.Links, EntityLink{URL: url, Title: title, Icon: icon})
}

func toID(name string) string {
	return strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(name))
}

func toTitle(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cloneAnnotations(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func toStrSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	var out []string
	if sl, ok := v.([]interface{}); ok {
		for _, item := range sl {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func sliceContains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func nameSet(lists ...[]string) map[string]bool {
	m := make(map[string]bool)
	for _, list := range lists {
		for _, name := range list {
			m[name] = true
		}
	}
	return m
}
