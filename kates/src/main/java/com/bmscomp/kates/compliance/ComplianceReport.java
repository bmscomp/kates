package com.bmscomp.kates.compliance;

import java.time.Instant;
import java.time.LocalDate;
import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * What a compliance check found: every rule the reference declares, with its
 * status, and a verdict the CLI turns into an exit code (PASS 0, FAIL 1,
 * UNKNOWN 2).
 */
public record ComplianceReport(
        String apiVersion,
        String kind,
        String reference,
        Target target,
        Instant checkedAt,
        ClusterState cluster,
        Verdict verdict,
        Summary summary,
        List<Result> results) {

    public enum Verdict {
        PASS,
        FAIL,
        UNKNOWN
    }

    public enum Status {
        PASS,
        FAIL,
        WARN,
        WAIVED,
        UNKNOWN
    }

    public record Target(String platform, String namespace, String name, String clusterId) {}

    /**
     * Context, not a rule: results may change while a cluster that is not
     * Ready, or not yet reconciled, settles.
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record ClusterState(Boolean ready, Long generation, Long observedGeneration, Integer podsNotReady) {}

    public record Summary(int pass, int fail, int warn, int waived, int unknown) {}

    /**
     * One rule. {@code observed} is the value the cluster has, or for a rule
     * over several subjects the values of those that fall short, which
     * {@code subjects} names.
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record Result(
            String rule,
            Status status,
            Severity severity,
            Map<String, Object> expected,
            Object observed,
            List<String> subjects,
            String detail,
            String source,
            Waived waiver,
            String note) {}

    /** The waiver that covers a rule, and the status the rule had without it. */
    public record Waived(Status status, String reason, String by, LocalDate until) {}
}
