package com.bmscomp.kates.security;

import java.util.List;

/**
 * The scopes an API key can carry (plans/mcp-server.md §5.1, §6.2). Each is a
 * role of the caller's {@code SecurityIdentity}, so an endpoint names the
 * scope it needs with {@code @RolesAllowed}. They are constants, not an enum,
 * because an annotation needs a compile-time constant.
 *
 * <p>{@link #CHAOS_RUN} is not in the plan, which has no scope for a human who
 * runs a fault directly rather than approving an agent's proposal.
 */
public final class Scopes {

    /** Every read of the v1 MCP tools, the dry run included. */
    public static final String READ = "read";
    /** Reads that expose data rather than state: records, secrets, the ACL map. */
    public static final String READ_SENSITIVE = "read:sensitive";
    /** Start and cancel a test run. */
    public static final String TEST_RUN = "test:run";
    /** Run a disruption, playbook, template or resilience test. Never an agent's. */
    public static final String CHAOS_RUN = "chaos:run";
    /** Propose a fault for a human to approve. */
    public static final String CHAOS_PROPOSE = "chaos:propose";
    /** Approve a proposed fault. Never an agent's. */
    public static final String CHAOS_APPROVE = "chaos:approve";
    /** Abort a running disruption. */
    public static final String ABORT = "abort";
    /** Topics, webhooks, schedules, baselines, profiles, deletes. Never an agent's. */
    public static final String ADMIN = "admin";

    public static final List<String> ALL =
            List.of(READ, READ_SENSITIVE, TEST_RUN, CHAOS_RUN, CHAOS_PROPOSE, CHAOS_APPROVE, ABORT, ADMIN);

    private Scopes() {}
}
