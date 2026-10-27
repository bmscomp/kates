package cmd

import (
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// mcpIdentifierFields names every plain-string field a tool result may carry
// unfenced, as "Type.Field". Each holds a value the Kates API or this server
// minted, in a fixed vocabulary or shape: an id, a status, a test type, a
// timestamp, a count rendered as text, or a number as the backend wrote it.
// Every other string a result can carry comes from a third party (a topic
// name, an alert annotation, a request path, a backend error message) and
// must be mcpUntrusted, built with call.Fence, so the output schema marks it
// and the guard checks it. A new string field fails
// TestMCPUnfencedStringsAreIdentifiers until it is fenced or, if it is an
// identifier, named here.
var mcpIdentifierFields = map[string]bool{
	// Ids, hashes and URIs the Kates API or this server minted.
	"mcpActivityDisruptionRow.ID":              true,
	"mcpAssessBand.RunIDs":                     true,
	"mcpAssessComparison.PreviousRunID":        true,
	"mcpAssessRegression.BaselineRunID":        true,
	"mcpAssessRunHead.ID":                      true,
	"mcpAssessRunHead.SpecHash":                true,
	"mcpCVE.ID":                                true,
	"mcpCatalogTemplate.ID":                    true,
	"mcpCaveatRef.ID":                          true,
	"mcpClusterOverviewOut.ClusterID":          true,
	"mcpClusterRef.ID":                         true,
	"mcpDisruptionReportComparison.BaselineID": true,
	"mcpDisruptionReportOut.ID":                true,
	"mcpDisruptionReportOut.TimelineURI":       true,
	"mcpGetRunOut.SpecHash":                    true,
	"mcpPentestTest.ID":                        true,
	"mcpPreviewOut.PlanSHA256":                 true,
	"mcpPreviewOut.Playbook":                   true,
	"mcpRunHead.ID":                            true,
	"mcpRunRow.ID":                             true,
	"mcpRunRow.SpecHash":                       true,

	// Fixed vocabularies: statuses, verdicts, grades, severities, test types, backends, metric and field names, counts rendered as text.
	"CreateTestRequest.Backend":                     true,
	"CreateTestRequest.TestType":                    true,
	"TestSpec.Acks":                                 true,
	"TestSpec.CompressionType":                      true,
	"mcpActivityAuditRow.Action":                    true,
	"mcpActivityAuditRow.EventType":                 true,
	"mcpActivityAuditRow.PrincipalType":             true,
	"mcpActivityDisruptionRow.SLAGrade":             true,
	"mcpActivityDisruptionRow.Status":               true,
	"mcpAgentLimitFinding.Field":                    true,
	"mcpAgentLimitFinding.Step":                     true,
	"mcpAgentLimitFinding.Value":                    true,
	"mcpAlertRule.Severity":                         true,
	"mcpAssessAdvice.Severity":                      true,
	"mcpAssessBand.MatchedOn":                       true,
	"mcpAssessBandMetric.Metric":                    true,
	"mcpAssessBandMetric.Position":                  true,
	"mcpAssessRegression.BaselineMatch":             true,
	"mcpAssessRunHead.Backend":                      true,
	"mcpAssessRunHead.Status":                       true,
	"mcpAssessRunHead.Type":                         true,
	"mcpCVE.Severity":                               true,
	"mcpCVE.Status":                                 true,
	"mcpCVEHeadline.Findings":                       true,
	"mcpCVEHeadline.Grade":                          true,
	"mcpCatalogPlaybook.Category":                   true,
	"mcpCatalogProvider.Status":                     true,
	"mcpCatalogTemplate.Category":                   true,
	"mcpCatalogTemplate.Severity":                   true,
	"mcpClusterHealth.Status":                       true,
	"mcpComplianceControl.Check":                    true,
	"mcpComplianceControl.Control":                  true,
	"mcpComplianceControl.Status":                   true,
	"mcpComplianceFramework.Compliance":             true,
	"mcpComplianceFramework.Framework":              true,
	"mcpConfigDiffHeadline.MismatchedKeys":          true,
	"mcpConsumerGroupLagOut.State":                  true,
	"mcpDelta.Metric":                               true,
	"mcpDisruptionReportComparisonSide.PassedSteps": true,
	"mcpDisruptionReportComparisonSide.SLAGrade":    true,
	"mcpDisruptionReportComparisonSide.Status":      true,
	"mcpDisruptionReportImpact.Dimensions (key)":    true,
	"mcpDisruptionReportImpact.NotScored":           true,
	"mcpDisruptionReportImpact.Severity":            true,
	"mcpDisruptionReportOut.Status":                 true,
	"mcpDisruptionReportSLA.Grade":                  true,
	"mcpDisruptionReportSLAViolation.Constraint":    true,
	"mcpDisruptionReportSLAViolation.Metric":        true,
	"mcpDisruptionReportSLAViolation.Severity":      true,
	"mcpDisruptionReportStep.DisruptionType":        true,
	"mcpDisruptionReportStep.ImpactDeltas (key)":    true,
	"mcpDisruptionReportStep.UnmeasuredMetrics":     true,
	"mcpDisruptionReportStep.Verdict":               true,
	"mcpDraftEffective.Defaulted":                   true,
	"mcpDraftEffective.Type":                        true,
	"mcpDraftScenarioOut.Verdict":                   true,
	"mcpDriftEntry.Baseline":                        true,
	"mcpDriftEntry.Change":                          true,
	"mcpDriftEntry.Check":                           true,
	"mcpDriftEntry.Current":                         true,
	"mcpDriftHeadline.BaselineGrade":                true,
	"mcpDriftHeadline.CurrentGrade":                 true,
	"mcpDriftHeadline.DegradedChecks":               true,
	"mcpDriftHeadline.NotInBaselineNotPassing":      true,
	"mcpGetRunOut.NotCarried":                       true,
	"mcpLagTopic.LeaderLookup":                      true,
	"mcpListRunsFilters.Status":                     true,
	"mcpListRunsFilters.Type":                       true,
	"mcpPartitionProblem.Issue":                     true,
	"mcpPentestHeadline.Findings":                   true,
	"mcpPentestTest.Result":                         true,
	"mcpPentestTest.Severity":                       true,
	"mcpPostureHeadline.Grade":                      true,
	"mcpPreviewOut.Source":                          true,
	"mcpPreviewStep.DisruptionType":                 true,
	"mcpPreviewStep.KRaftControllers":               true,
	"mcpPreviewStep.Targeting":                      true,
	"mcpQuorum.Issue":                               true,
	"mcpResult.Tier":                                true,
	"mcpRunChange.Metric":                           true,
	"mcpRunHead.Backend":                            true,
	"mcpRunHead.Status":                             true,
	"mcpRunHead.Type":                               true,
	"mcpRunIntegrityEventOut.Type":                  true,
	"mcpRunIntegrityOut.Verdict":                    true,
	"mcpRunRequestedSpec.Acks":                      true,
	"mcpRunRequestedSpec.CompressionType":           true,
	"mcpRunRow.Backend":                             true,
	"mcpRunRow.Status":                              true,
	"mcpRunRow.Type":                                true,
	"mcpRunSpec.Acks":                               true,
	"mcpRunSpec.CompressionType":                    true,
	"mcpRunTaskOut.Status":                          true,
	"mcpScnEnvelope.Source":                         true,
	"mcpScnEnvelope.Types":                          true,
	"mcpScnFinding.Field":                           true,
	"mcpScnFinding.Level":                           true,
	"mcpSecCheckRef.Severity":                       true,
	"mcpSecCheckRef.Status":                         true,
	"mcpSecurityCVE.Grade":                          true,
	"mcpSecurityCheck.Category":                     true,
	"mcpSecurityCheck.Control":                      true,
	"mcpSecurityCheck.Severity":                     true,
	"mcpSecurityCheck.Status":                       true,
	"mcpSecurityCompliance.Grade":                   true,
	"mcpSecurityConfigDiff.Grade":                   true,
	"mcpSecurityDrift.BaselineGrade":                true,
	"mcpSecurityDrift.CurrentGrade":                 true,
	"mcpSecurityEvidenceOut.Section":                true,
	"mcpSecurityPosture.Grade":                      true,
	"mcpTopicLayout.MinISRSource":                   true,
	"mcpTopologyPool.Roles":                         true,
	"mcpTopologyUnread.Section":                     true,

	// Timestamps and versions as the backend wrote them.
	"mcpActivityAuditRow.Timestamp":      true,
	"mcpActivityDisruptionRow.CreatedAt": true,
	"mcpActivityOut.Since":               true,
	"mcpAssessRunHead.CreatedAt":         true,
	"mcpCVE.AffectedFrom":                true,
	"mcpCVE.AffectedUpTo":                true,
	"mcpCVEHeadline.KafkaVersion":        true,
	"mcpDisruptionReportStep.FaultEnd":   true,
	"mcpDisruptionReportStep.FaultStart": true,
	"mcpDriftHeadline.BaselineTimestamp": true,
	"mcpRunHead.CreatedAt":               true,
	"mcpRunRow.CreatedAt":                true,
	"mcpRunTaskOut.EndTime":              true,
	"mcpRunTaskOut.StartTime":            true,
	"mcpSecurityCVE.KafkaVersion":        true,
	"mcpSecurityCerts.Timestamp":         true,
	"mcpSecurityChecks.Timestamp":        true,
	"mcpSecurityCompliance.Timestamp":    true,
	"mcpSecurityDrift.BaselineTimestamp": true,
	"mcpSecurityPentest.Timestamp":       true,
	"mcpSecurityPosture.Timestamp":       true,
	"mcpTopologyKafka.KafkaVersion":      true,

	// Text this server or the Kates API writes itself: reasons, caveats, the plan or scenario it built from the caller's input.
	"mcpAssessBand.Reason":           true,
	"mcpAssessBrokers.Reason":        true,
	"mcpAssessComparison.Reason":     true,
	"mcpAssessRegression.Reason":     true,
	"mcpCaveatRef.Text":              true,
	"mcpClusterRef.Label":            true,
	"mcpDraftScenarioOut.YAML":       true,
	"mcpPreviewControllers.Reason":   true,
	"mcpPreviewOut.PlanJSON":         true,
	"mcpRunIntegrityEventOut.Detail": true,
	"mcpScnFinding.Message":          true,
	"mcpSecHeadline.Reason":          true,

	// Kafka and Kubernetes resource names, which mcpSanitizeLine cleans and bounds: topics, pools, pods, racks, nodes. They are identifiers the cluster's operator chose; a maintainer who wants them fenced moves them to mcpUntrusted.
	"TestSpec.ConsumerGroup":                 true,
	"TestSpec.Topic":                         true,
	"mcpAlertRule.Source":                    true,
	"mcpBroker.Host":                         true,
	"mcpBroker.Rack":                         true,
	"mcpCatalogFaultType.Name":               true,
	"mcpCatalogPlaybook.Name":                true,
	"mcpCatalogProvider.Name":                true,
	"mcpComplianceFramework.Name":            true,
	"mcpConsumerGroupLagOut.CommittedTopics": true,
	"mcpConsumerGroupLagOut.Topic":           true,
	"mcpDraftEffective.Topic":                true,
	"mcpDraftedScenario.Name":                true,
	"mcpLagPartition.Topic":                  true,
	"mcpLagTopic.Topic":                      true,
	"mcpPartitionProblem.Topic":              true,
	"mcpPentestTest.Name":                    true,
	"mcpPreviewControllers.Touched":          true,
	"mcpPreviewOut.PlanName":                 true,
	"mcpPreviewStep.AffectedPods":            true,
	"mcpPreviewStep.Name":                    true,
	"mcpRunRequestedSpec.Topic":              true,
	"mcpRunSpec.Topic":                       true,
	"mcpScnEnvelope.TopicPrefix":             true,
	"mcpSecBrokerValue.Broker":               true,
	"mcpSecCheckRef.Name":                    true,
	"mcpSecConfigKey.Key":                    true,
	"mcpSecurityCheck.Name":                  true,
	"mcpTopicLayout.Name":                    true,
	"mcpTopologyBroker.Host":                 true,
	"mcpTopologyBroker.K8sNode":              true,
	"mcpTopologyBroker.Pool":                 true,
	"mcpTopologyBroker.Rack":                 true,
	"mcpTopologyKafka.Name":                  true,
	"mcpTopologyKafka.Namespace":             true,
	"mcpTopologyPod.K8sNode":                 true,
	"mcpTopologyPod.Pool":                    true,
	"mcpTopologyPool.Name":                   true,
	"mcpTopologyPool.StorageClass":           true,
	"mcpTopologyPool.StorageSize":            true,
	"mcpTopologyPool.StorageType":            true,

	// An audit row's target is an id, or the request path inside a fence
	// (mcpActivityTarget); its actor is the principal of the API key used, as
	// GET /api/whoami names it, cleaned and bounded.
	"mcpActivityAuditRow.Target": true,
	"mcpActivityAuditRow.Actor":  true,
}

// mcpOutputStringFields walks every type reachable from the registered
// tools' Out types, and the result envelope, and returns the plain-string
// and interface{} fields found, as "Type.Field", with a map's string keys as
// "Type.Field (key)".
func mcpOutputStringFields(t *testing.T) []string {
	t.Helper()
	mcpToolOutputsMu.Lock()
	outs := maps.Clone(mcpToolOutputTypes)
	mcpToolOutputsMu.Unlock()
	if len(outs) == 0 {
		t.Fatal("no tool registered its output type")
	}

	plainString := reflect.TypeOf("")
	seen := map[reflect.Type]bool{}
	found := map[string]bool{}
	var walk func(rt reflect.Type, at string)
	walk = func(rt reflect.Type, at string) {
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			if rt.Kind() != reflect.Pointer && rt.Elem().Kind() == reflect.Uint8 {
				return // []byte and json.RawMessage
			}
			walk(rt.Elem(), at)
		case reflect.Map:
			if rt.Key() == plainString {
				found[at+" (key)"] = true
			}
			walk(rt.Elem(), at)
		case reflect.String:
			if rt == plainString {
				found[at] = true
			}
		case reflect.Interface:
			found[at] = true
		case reflect.Struct:
			if seen[rt] {
				return
			}
			seen[rt] = true
			name := rt.Name()
			if i := strings.Index(name, "["); i > 0 {
				name = name[:i] // a generic instantiation, without its type argument
			}
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if !f.IsExported() && !f.Anonymous {
					continue
				}
				walk(f.Type, name+"."+f.Name)
			}
		}
	}
	walk(reflect.TypeFor[mcpResult[struct{}]](), "mcpResult")
	for _, name := range slices.Sorted(maps.Keys(outs)) {
		walk(outs[name], name)
	}
	return slices.Sorted(maps.Keys(found))
}

// TestMCPUnfencedStringsAreIdentifiers is the contract behind mcpUntrusted:
// a string a third party can write never reaches the model as a plain field.
// kates_activity used to return the caller-controlled request path in
// audit[].target, and cluster_overview an alert rule's name and source, as
// plain strings; the allow-list is what is left, each entry an identifier.
func TestMCPUnfencedStringsAreIdentifiers(t *testing.T) {
	newMCPHarness(t, newMCPFakeBackend(t, "cluster-a")) // registers every tool
	fields := mcpOutputStringFields(t)

	var unlisted []string
	for _, f := range fields {
		if !mcpIdentifierFields[f] {
			unlisted = append(unlisted, f)
		}
	}
	if len(unlisted) > 0 {
		t.Errorf("%d plain-string fields reach the model unfenced and are not on mcpIdentifierFields; fence each with call.Fence, or name it there if it is an identifier the backend minted:\n  %s",
			len(unlisted), strings.Join(unlisted, "\n  "))
	}
	for name := range mcpIdentifierFields {
		if !slices.Contains(fields, name) {
			t.Errorf("mcpIdentifierFields names %s, which no tool output has (renamed, fenced or removed?)", name)
		}
	}
}

// An audit row's target is the id of a run or disruption, or the request
// path, which the caller chose. The id goes through as it is, so the model
// can hand it to another tool; the path is fenced, so prose planted in it
// reads as data. It used to be sanitised only.
func TestMCPKatesActivityTargetFencedUnlessID(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	mcpServeRuns(fb, nil)
	fb.JSON("GET", "/api/disruptions", http.StatusOK, map[string]any{"items": []any{}})
	fb.JSON("GET", "/api/audit", http.StatusOK, map[string]any{
		"total": 3,
		"items": []map[string]any{
			{"id": 3, "action": "CANCEL", "eventType": "test", "target": "0000000c", "timestamp": "2026-09-25T11:40:00Z"},
			{"id": 2, "action": "READ", "eventType": "test", "target": "/api/tests?note=" + mcpInjection, "timestamp": "2026-09-25T11:35:00Z"},
			{"id": 1, "action": "READ", "eventType": "test", "target": "0000000C", "timestamp": "2026-09-25T11:30:00Z"},
		},
	})
	h := newMCPHarness(t, fb, withMCPClock(newMCPFakeClock().Now))
	got := mcpData[mcpActivityOut](t, h.callOK("kates_activity", nil))
	rows := got.Audit.Rows
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Target != "0000000c" {
		t.Errorf("an id is fenced or changed: %q", rows[0].Target)
	}
	for _, row := range rows[1:] {
		if !mcpFenced(h, mcpUntrusted(row.Target)) {
			t.Errorf("row %d: target %q is not fenced, and it is not an id", row.ID, row.Target)
		}
	}
	if strings.ContainsAny(rows[1].Target, "\x1b\u202e\u200b") || strings.Count(rows[1].Target, mcpFenceClosePrefix) != 1 {
		t.Errorf("target not sanitised: %q", rows[1].Target)
	}
	if !strings.Contains(rows[1].Target, "/api/tests?note=") {
		t.Errorf("the path is lost: %q", rows[1].Target)
	}
}
