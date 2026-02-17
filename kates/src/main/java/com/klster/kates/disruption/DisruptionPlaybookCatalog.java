package com.klster.kates.disruption;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLFactory;
import com.klster.kates.chaos.DisruptionType;
import com.klster.kates.chaos.FaultSpec;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;

import java.io.IOException;
import java.io.InputStream;
import java.util.*;
import org.jboss.logging.Logger;

/**
 * Loads pre-built disruption playbooks from YAML resource files.
 * Each playbook is a curated scenario converted to a DisruptionPlan.
 */
@ApplicationScoped
public class DisruptionPlaybookCatalog {

    private static final Logger LOG = Logger.getLogger(DisruptionPlaybookCatalog.class);
    private static final ObjectMapper YAML = new ObjectMapper(new YAMLFactory());

    private static final String[] PLAYBOOK_NAMES = {
            "az-failure", "split-brain", "storage-pressure",
            "rolling-restart", "leader-cascade", "consumer-isolation"
    };

    private final Map<String, PlaybookEntry> catalog = new LinkedHashMap<>();

    @PostConstruct
    void loadPlaybooks() {
        for (String name : PLAYBOOK_NAMES) {
            String path = "playbooks/" + name + ".yaml";
            try (InputStream is = Thread.currentThread().getContextClassLoader().getResourceAsStream(path)) {
                if (is == null) {
                    LOG.warn("Playbook resource not found: " + path);
                    continue;
                }
                PlaybookEntry entry = YAML.readValue(is, PlaybookEntry.class);
                catalog.put(entry.name, entry);
                LOG.info("Loaded playbook: " + entry.name + " (" + entry.category + ")");
            } catch (IOException e) {
                LOG.warn("Failed to load playbook: " + path, e);
            }
        }
        LOG.info("Playbook catalog loaded: " + catalog.size() + " playbooks");
    }

    public List<PlaybookEntry> listAll() {
        return List.copyOf(catalog.values());
    }

    public Optional<PlaybookEntry> findByName(String name) {
        return Optional.ofNullable(catalog.get(name));
    }

    public List<PlaybookEntry> findByCategory(String category) {
        return catalog.values().stream()
                .filter(p -> category.equalsIgnoreCase(p.category))
                .toList();
    }

    public DisruptionPlan toPlan(PlaybookEntry entry) {
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("playbook:" + entry.name);
        plan.setMaxAffectedBrokers(entry.maxAffectedBrokers);
        plan.setAutoRollback(entry.autoRollback);

        if (entry.isrTrackingTopic != null) {
            plan.setIsrTrackingTopic(entry.isrTrackingTopic);
        }

        List<DisruptionPlan.DisruptionStep> steps = new ArrayList<>();
        if (entry.steps != null) {
            for (PlaybookStep ps : entry.steps) {
                FaultSpec faultSpec = null;
                if (ps.faultSpec != null) {
                    FaultSpec.Builder fb = FaultSpec.builder(ps.faultSpec.experimentName)
                            .chaosDurationSec(ps.faultSpec.chaosDurationSec);

                    if (ps.faultSpec.disruptionType != null) {
                        fb.disruptionType(DisruptionType.valueOf(ps.faultSpec.disruptionType));
                    }
                    if (ps.faultSpec.targetLabel != null) {
                        fb.targetLabel(ps.faultSpec.targetLabel);
                    }
                    if (ps.faultSpec.targetNamespace != null) {
                        fb.targetNamespace(ps.faultSpec.targetNamespace);
                    }
                    if (ps.faultSpec.gracePeriodSec != null) {
                        fb.gracePeriodSec(ps.faultSpec.gracePeriodSec);
                    }
                    if (ps.faultSpec.targetBrokerId != null) {
                        fb.targetBrokerId(ps.faultSpec.targetBrokerId);
                    }
                    if (ps.faultSpec.targetTopic != null) {
                        fb.targetTopic(ps.faultSpec.targetTopic);
                    }
                    if (ps.faultSpec.targetPartition != null) {
                        fb.targetPartition(ps.faultSpec.targetPartition);
                    }
                    if (ps.faultSpec.fillPercentage != null) {
                        fb.fillPercentage(ps.faultSpec.fillPercentage);
                    }
                    faultSpec = fb.build();
                }

                steps.add(new DisruptionPlan.DisruptionStep(
                        ps.name, faultSpec,
                        ps.steadyStateSec, ps.observationWindowSec,
                        ps.requireRecovery));
            }
        }
        plan.setSteps(steps);
        return plan;
    }

    public static class PlaybookEntry {
        public String name;
        public String description;
        public String category;
        public int maxAffectedBrokers = -1;
        public boolean autoRollback = true;
        public String isrTrackingTopic;
        public List<PlaybookStep> steps;
    }

    public static class PlaybookStep {
        public String name;
        public PlaybookFaultSpec faultSpec;
        public int steadyStateSec;
        public int observationWindowSec;
        public boolean requireRecovery;
    }

    public static class PlaybookFaultSpec {
        public String experimentName;
        public String disruptionType;
        public String targetLabel;
        public String targetNamespace;
        public Integer targetBrokerId;
        public String targetTopic;
        public Integer targetPartition;
        public int chaosDurationSec;
        public Integer gracePeriodSec;
        public Integer fillPercentage;
    }
}
