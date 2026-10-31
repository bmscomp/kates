# Index {.unnumbered}

::: {.content-visible when-format="pdf"}
```{=latex}
\printindex
```
:::

::: {.content-visible when-format="html"}
Every term the book teaches, with the sections that teach it: the ones that use the term more than once or name it in their heading, by chapter and in reading order. The [Glossary](appendix-a-glossary.md) defines each term; this page says where to read about it. The PDF edition lists the same entries by page number.

## A

[acks]{#idx-acks}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Test Types Deep Dive](05-test-types.md): [SPIKE Test](05-test-types.md#spike-test); [Lab — Interactive Performance Tuning](10b-lab.md): [Presets](10b-lab.md#presets); [Data Integrity Verification](08-data-integrity.md): [Integrity Modes](08-data-integrity.md#integrity-modes), [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results), [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[ACL]{#idx-acl}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Security & Compliance](17-security.md): [Authorization](17-security.md#authorization); [Multi-Tenancy](19-multi-tenancy.md): [Multi-Tenancy Model](19-multi-tenancy.md#multi-tenancy-model); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Admission Webhook]{#idx-admission-webhook}
:   [Observability & Monitoring](09-observability.md): [The Dashboard Set](09-observability.md#the-dashboard-set); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components)

[API Key]{#idx-api-key}
:   [Introduction](01-introduction.md): [Quick Start](01-introduction.md#quick-start); [The Cluster Under Test](03-cluster.md): [Access Points](03-cluster.md#access-points); [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [Security & Compliance](17-security.md): [Securing Kates](17-security.md#securing-kates); [CLI Reference](10-cli-reference.md): [Configuration](10-cli-reference.md#configuration), [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Authentication](11-api-reference.md#authentication); [Troubleshooting Index](appendix-b-troubleshooting.md): [CLI Issues](appendix-b-troubleshooting.md#cli-issues)

[Apicurio Registry]{#idx-apicurio-registry}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Schema Registry Integration](21-kafka-connect.md#schema-registry-integration)

[appVersion]{#idx-appversion}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation)

[Availability Zone]{#idx-availability-zone}
:   [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology)

[AZ]{#idx-az}
:   [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Recipes & Patterns](14-recipes.md): [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

## B

[Baseline]{#idx-baseline}
:   [Architecture & Design](02-architecture.md): [Where Results Live](02-architecture.md#where-results-live); [Performance Theory](04-performance-theory.md): [Telling a Regression from Noise](04-performance-theory.md#statistical-significance); [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Chaos Engineering Theory](06-chaos-theory.md): [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security); [Recipes & Patterns](14-recipes.md): [Recipe 1: Validate a Kafka Upgrade](14-recipes.md#recipe-1-validate-a-kafka-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Benchmark Backend]{#idx-benchmark-backend}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [The Kates API](02-architecture.md#the-kates-api); [Observability & Monitoring](09-observability.md): [The Meters Kates Publishes](09-observability.md#the-meters-kates-publishes); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [gRPC API Reference](16-grpc-api.md): [Service Definitions](16-grpc-api.md#service-definitions)

[Blast Radius]{#idx-blast-radius}
:   [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles)

[Broker]{#idx-broker}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration), [Changing the Topology for Different Scenarios](03-cluster.md#changing-the-topology-for-different-scenarios); [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Compliance Language Reference](appendix-compliance-language.md): [Kafka Settings](appendix-compliance-language.md#kafka-settings)

## C

[CDC]{#idx-cdc}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Kafka Connect & CDC Pipelines](21-kafka-connect.md): Kafka Connect & CDC Pipelines, [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium), [CDC Patterns](21-kafka-connect.md#cdc-patterns); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Version & Compatibility Matrix](appendix-d-versions.md): Version & Compatibility Matrix

[Change Data Capture]{#idx-change-data-capture}
:   See *CDC*.

[Chaos Backend]{#idx-chaos-backend}
:   Not used in a chapter yet.

[Chaos Experiment]{#idx-chaos-experiment}
:   [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Chaos Provider]{#idx-chaos-provider}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [Disruption Engine](02-architecture.md#disruption-engine); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Disruption Types](07-chaos-practice.md#disruption-types), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Recipes & Patterns](14-recipes.md): [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Clients CA]{#idx-clients-ca}
:   [Security & Compliance](17-security.md): [Certificate Management](17-security.md#certificate-management)

[Cluster CA]{#idx-cluster-ca}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features); [Operating Kafka Connect](operating-kafka-connect.md): Security & Credential Rotation

[ClusterPolicy]{#idx-clusterpolicy}
:   [Security & Compliance](17-security.md): [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Committed Offset]{#idx-committed-offset}
:   [Architecture & Design](02-architecture.md): [Where Results Live](02-architecture.md#where-results-live); [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book)

[Compliance Check]{#idx-compliance-check}
:   [Deployment Guide](12-deployment.md): [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [Compliance Checks](compliance.md): [Why Compliance](compliance.md#why-compliance), [Installing Kates for Compliance Checks](compliance.md#installing-for-compliance); [Recipes & Patterns](14-recipes.md): [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [Compliance Language Reference](appendix-compliance-language.md): [The Document](appendix-compliance-language.md#the-document), [The Report](appendix-compliance-language.md#the-report)

[Connector]{#idx-connector}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [How Connect Divides the Work](21-kafka-connect.md#how-connect-divides-the-work), [Connector Lifecycle](21-kafka-connect.md#connector-lifecycle), [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium); [Operating Kafka Connect](operating-kafka-connect.md): REST API & Connector Operations, Security & Credential Rotation, [Troubleshooting](operating-kafka-connect.md#troubleshooting); [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Why Migration Is the Hard Case](22-mirror-maker2-migration.md#why-migration-is-the-hard-case); [Troubleshooting Index](appendix-b-troubleshooting.md): [Kafka Connect](appendix-b-troubleshooting.md#kafka-connect)

[Consumer Group]{#idx-consumer-group}
:   [Chaos Engineering Theory](06-chaos-theory.md): [Kafka-Specific Failure Modes](06-chaos-theory.md#kafka-specific-failure-modes); [Chaos Engineering in Practice](07-chaos-practice.md): [Kafka Metrics](07-chaos-practice.md#kafka-intelligence); [Security & Compliance](17-security.md): [Authorization](17-security.md#authorization); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [CDC Patterns](21-kafka-connect.md#cdc-patterns); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Consumer Lag]{#idx-consumer-lag}
:   [Chaos Engineering Theory](06-chaos-theory.md): [Key Metrics During Chaos](06-chaos-theory.md#key-metrics-during-chaos); [Observability & Monitoring](09-observability.md): [Alerting](09-observability.md#alerting); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components)

[Context]{#idx-context}
:   [Preface](index.qmd): [Before You Start](index.qmd#before-you-start); [Introduction](01-introduction.md): [Quick Start](01-introduction.md#quick-start); [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Deployment Guide](12-deployment.md): [CLI Configuration](12-deployment.md#cli-configuration); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security); [CLI Reference](10-cli-reference.md): [Configuration](10-cli-reference.md#configuration), [Global Flags](10-cli-reference.md#global-flags), [Commands](10-cli-reference.md#commands)

[Controller]{#idx-controller}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book), [Replication Configuration](03-cluster.md#replication-configuration); [Chaos Engineering in Practice](07-chaos-practice.md): [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Deployment Guide](12-deployment.md): [Cloud Deployment](12-deployment.md#cloud-deployment); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Coordinated Omission]{#idx-coordinated-omission}
:   [Performance Theory](04-performance-theory.md): [Coordinated Omission](04-performance-theory.md#coordinated-omission)

[Cosign]{#idx-cosign}
:   [Deployment Guide](12-deployment.md): [Deploying Without GHCR](12-deployment.md#deploying-without-ghcr); [Security & Compliance](17-security.md): [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [CI/CD Pipeline](appendix-c-cicd.md): Publish Kates Docker Image (`publish-docker.yml`)

[CRC32]{#idx-crc32}
:   [Data Integrity Verification](08-data-integrity.md): [Sequence Number Tracking](08-data-integrity.md#sequence-number-tracking)

[CRD]{#idx-crd}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Why a Separate Release](deploying-strimzi-operator.md#why-a-separate-release), [The CRD Lifecycle Gap](deploying-strimzi-operator.md#the-crd-lifecycle-gap), [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator), [Upgrading the Operator](deploying-strimzi-operator.md#upgrading-the-operator), [Troubleshooting](deploying-strimzi-operator.md#troubleshooting); [Upgrade Playbook](18-upgrade-playbook.md): [Strimzi Operator Upgrade](18-upgrade-playbook.md#strimzi-operator-upgrade)

[Cruise Control]{#idx-cruise-control}
:   [The Cluster Under Test](03-cluster.md): [Changing the Topology for Different Scenarios](03-cluster.md#changing-the-topology-for-different-scenarios); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Environment Overlays](20-installation-guide.md#environment-overlays), [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), [Network Policies](20-installation-guide.md#network-policies), [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components), [Troubleshooting](15-kafka-deployment.md#troubleshooting); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies)

[Custom Resource]{#idx-custom-resource}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): Deploying the Strimzi Operator; [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [The KafkaConnect Custom Resource](21-kafka-connect.md#the-kafkaconnect-custom-resource)

## D

[Dead Letter Queue]{#idx-dead-letter-queue}
:   See *DLQ*.

[Debezium]{#idx-debezium}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Kafka Connect & CDC Pipelines](21-kafka-connect.md): Kafka Connect & CDC Pipelines, [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium), [Single Message Transforms (SMTs)](21-kafka-connect.md#single-message-transforms-smts), [Pre-Deploy Validation](21-kafka-connect.md#pre-deploy-validation); [Operating Kafka Connect](operating-kafka-connect.md): [Performance Tuning](operating-kafka-connect.md#performance-tuning), [Upgrade Procedures](operating-kafka-connect.md#upgrade-procedures); [CI/CD Pipeline](appendix-c-cicd.md): Publish Kafka Connect Image (`publish-connect.yml`)

[Disruption]{#idx-disruption}
:   [Architecture & Design](02-architecture.md): [Disruption Engine](02-architecture.md#disruption-engine); [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Disruption Types](07-chaos-practice.md#disruption-types), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [Recipes & Patterns](14-recipes.md): [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Disruption Plan]{#idx-disruption-plan}
:   [Introduction](01-introduction.md): [Design Philosophy](01-introduction.md#design-philosophy); [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Disruption Rollback]{#idx-disruption-rollback}
:   Not used in a chapter yet.

[Disruption Type]{#idx-disruption-type}
:   [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[DLQ]{#idx-dlq}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Kafka Deployment Engineering](15-kafka-deployment.md): [Topic Provisioning](15-kafka-deployment.md#topic-provisioning); [Deployment Guide](12-deployment.md): [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [Multi-Tenancy](19-multi-tenancy.md): [Topic Naming Convention](19-multi-tenancy.md#topic-naming-convention); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Dead Letter Queue (DLQ)](21-kafka-connect.md#dead-letter-queue-dlq)

[Drain Cleaner]{#idx-drain-cleaner}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [What the Chart Deploys](deploying-strimzi-operator.md#what-the-chart-deploys), [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components); [Upgrade Playbook](18-upgrade-playbook.md): [Strimzi Operator Upgrade](18-upgrade-playbook.md#strimzi-operator-upgrade)

## E

[Eligible Leader Replicas]{#idx-eligible-leader-replicas}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration)

[emptyDir]{#idx-emptydir}
:   [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment)

[Entity Operator]{#idx-entity-operator}
:   [The Cluster Under Test](03-cluster.md): [Operational Components](03-cluster.md#operational-components); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), Topics & Users Reference, [Network Policies](20-installation-guide.md#network-policies), [Troubleshooting](20-installation-guide.md#troubleshooting); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components), [Troubleshooting](15-kafka-deployment.md#troubleshooting); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies)

[Exactly-Once Semantics]{#idx-exactly-once-semantics}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Exactly-Once Semantics](21-kafka-connect.md#exactly-once-semantics)

[Exit Code]{#idx-exit-code}
:   [Scenario Files & SLA Gates](13-scenario-files.md): [Running Scenario Files](13-scenario-files.md#running-scenario-files), [CI/CD Integration](13-scenario-files.md#cicd-integration); [Compliance Checks](compliance.md): [Why Compliance](compliance.md#why-compliance); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands), [Exit Codes](10-cli-reference.md#exit-codes)

## F

[Fault]{#idx-fault}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Disruption Types](07-chaos-practice.md#disruption-types), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks), [Safety Guardrails](07-chaos-practice.md#safety-guardrails), [Resilience Runs: A Fault Inside a Test](07-chaos-practice.md#resilience-runs-a-fault-inside-a-test); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Fencing]{#idx-fencing}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration)

## G

[Game Day]{#idx-game-day}
:   [Chaos Engineering Theory](06-chaos-theory.md): [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology); [Deployment Guide](12-deployment.md): [Makefile Reference](12-deployment.md#makefile-reference), [Game Day Validation](12-deployment.md#game-day-validation)

[Gate]{#idx-gate}
:   [Introduction](01-introduction.md): [How Kates Fits into Your Workflow](01-introduction.md#how-kates-fits-into-your-workflow); [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates), [Running Scenario Files](13-scenario-files.md#running-scenario-files), [Checking a File Before It Runs](13-scenario-files.md#checking-a-file-before-it-runs), [Common Mistakes](13-scenario-files.md#common-mistakes); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Recipes & Patterns](14-recipes.md): [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[GraalVM Native Image]{#idx-graalvm-native-image}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Lab — Interactive Performance Tuning](10b-lab.md): [Warm-Up Mode (`W`)](10b-lab.md#warmup-mode-w)

[Grade]{#idx-grade}
:   [Chaos Engineering Theory](06-chaos-theory.md): [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology); [Chaos Engineering in Practice](07-chaos-practice.md): Chaos Engineering in Practice, [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [SLA Grading](07-chaos-practice.md#sla-grading); [Recipes & Patterns](14-recipes.md): [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka), [Recipe 8: Sign Off krafter for Payments](14-recipes.md#recipe-8-sign-off-krafter-for-payments); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Grafana]{#idx-grafana}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [The Cluster Under Test](03-cluster.md): [Access Points](03-cluster.md#access-points); [Observability & Monitoring](09-observability.md): [Monitoring Stack Deployment](09-observability.md#monitoring-stack-deployment); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Observability Stack](20-installation-guide.md#observability-stack); [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions), [Cloud Deployment](12-deployment.md#cloud-deployment), [Makefile Reference](12-deployment.md#makefile-reference); [Operating Kafka Connect](operating-kafka-connect.md): [Observability](operating-kafka-connect.md#observability)

[gRPC]{#idx-grpc}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [The Kates API](02-architecture.md#the-kates-api); [Observability & Monitoring](09-observability.md): [Distributed Tracing](09-observability.md#distributed-tracing); [REST API Reference](11-api-reference.md): [Introduction](11-api-reference.md#introduction); [gRPC API Reference](16-grpc-api.md): [Choosing gRPC or REST](16-grpc-api.md#grpc-vs-rest), [Connection](16-grpc-api.md#connection), [Error Handling](16-grpc-api.md#error-handling), [Client Code Generation](16-grpc-api.md#client-code-generation)

[Guardrail]{#idx-guardrail}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails)

## H

[Heatmap]{#idx-heatmap}
:   [Architecture & Design](02-architecture.md): [The Kates API](02-architecture.md#the-kates-api), [Where Results Live](02-architecture.md#where-results-live); [Performance Theory](04-performance-theory.md): [Coordinated Omission](04-performance-theory.md#coordinated-omission); [Observability & Monitoring](09-observability.md): [Latency Heatmaps](09-observability.md#latency-heatmaps), [Exporting a Report](09-observability.md#exporting-a-report); [Recipes & Patterns](14-recipes.md): [Recipe 4: Investigate a Latency Regression](14-recipes.md#recipe-4-investigate-a-latency-regression); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Helm Release]{#idx-helm-release}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Helm Test]{#idx-helm-test}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Network Policies](20-installation-guide.md#network-policies), [Optional Features](20-installation-guide.md#advanced-features); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies), [Container Security](17-security.md#container-security), [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium); [Operating Kafka Connect](operating-kafka-connect.md): REST API & Connector Operations; [CI/CD Pipeline](appendix-c-cicd.md): Backend CI (`ci.yml`)

[High Watermark]{#idx-high-watermark}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration)

## I

[Idempotent Producer]{#idx-idempotent-producer}
:   Not used in a chapter yet.

[Isolated Topology]{#idx-isolated-topology}
:   [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[ISR]{#idx-isr}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Chaos Engineering Theory](06-chaos-theory.md): [Kafka-Specific Failure Modes](06-chaos-theory.md#kafka-specific-failure-modes); [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails), [Kafka Metrics](07-chaos-practice.md#kafka-intelligence); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Observability & Monitoring](09-observability.md): [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Compliance Language Reference](appendix-compliance-language.md): [Lint Findings](appendix-compliance-language.md#lint-findings)

[ISR Shrink]{#idx-isr-shrink}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Chaos Engineering Theory](06-chaos-theory.md): [Kafka-Specific Failure Modes](06-chaos-theory.md#kafka-specific-failure-modes)

## J

[Jaeger]{#idx-jaeger}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Observability & Monitoring](09-observability.md): [Distributed Tracing](09-observability.md#distributed-tracing); [Deployment Guide](12-deployment.md): [Makefile Reference](12-deployment.md#makefile-reference)

[JMX]{#idx-jmx}
:   [Observability & Monitoring](09-observability.md): [Observability Architecture](09-observability.md#observability-architecture), [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Observability Stack](20-installation-guide.md#observability-stack); [Multi-Tenancy](19-multi-tenancy.md): [Monitoring a Tenant](19-multi-tenancy.md#monitoring-a-tenant)

## K

[Kafka Connect]{#idx-kafka-connect}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), Topics & Users Reference; [Kafka Connect & CDC Pipelines](21-kafka-connect.md): Kafka Connect & CDC Pipelines, [Connector Lifecycle](21-kafka-connect.md#connector-lifecycle); [Operating Kafka Connect](operating-kafka-connect.md): Security & Credential Rotation; [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Troubleshooting Index](appendix-b-troubleshooting.md): [Kafka Connect](appendix-b-troubleshooting.md#kafka-connect); [CI/CD Pipeline](appendix-c-cicd.md): Publish Kafka Connect Image (`publish-connect.yml`)

[Kafka Exporter]{#idx-kafka-exporter}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Environment Overlays](20-installation-guide.md#environment-overlays), [Network Policies](20-installation-guide.md#network-policies); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components); [Compliance Checks](compliance.md): [Kafka Settings and Monitoring](compliance.md#kafka-settings-and-monitoring); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies)

[Kafka UI]{#idx-kafka-ui}
:   [Kafka Deployment Engineering](15-kafka-deployment.md): [Troubleshooting](15-kafka-deployment.md#troubleshooting); [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Troubleshooting Index](appendix-b-troubleshooting.md): [Kafka Connectivity](appendix-b-troubleshooting.md#kafka-connectivity)

[KafkaConnector]{#idx-kafkaconnector}
:   [Operating Kafka Connect](operating-kafka-connect.md): REST API & Connector Operations; [Troubleshooting Index](appendix-b-troubleshooting.md): [Kafka Connect](appendix-b-troubleshooting.md#kafka-connect)

[KafkaNodePool]{#idx-kafkanodepool}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [How the Operator Works](deploying-strimzi-operator.md#how-the-operator-works), [Rollback](deploying-strimzi-operator.md#rollback); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), [Common Customizations](20-installation-guide.md#common-customizations); [Kafka Deployment Engineering](15-kafka-deployment.md): [KafkaNodePool Configuration](15-kafka-deployment.md#kafkanodepool-configuration)

[KafkaRebalance]{#idx-kafkarebalance}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features)

[KafkaReference]{#idx-kafkareference}
:   [Compliance Language Reference](appendix-compliance-language.md): [The Document](appendix-compliance-language.md#the-document)

[KafkaTopic]{#idx-kafkatopic}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Topic Provisioning](15-kafka-deployment.md#topic-provisioning), [Operational Components](15-kafka-deployment.md#operational-components)

[KafkaUser]{#idx-kafkauser}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Kafka Deployment Engineering](15-kafka-deployment.md): [KafkaUser Management](15-kafka-deployment.md#kafkauser-management); [Deployment Guide](12-deployment.md): [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [Compliance Checks](compliance.md): [Installing Kates for Compliance Checks](compliance.md#installing-for-compliance); [Security & Compliance](17-security.md): [Authentication](17-security.md#authentication), [Authorization](17-security.md#authorization); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Helm Chart Structure](21-kafka-connect.md#helm-chart-structure), [The KafkaConnect Custom Resource](21-kafka-connect.md#the-kafkaconnect-custom-resource)

[Kates API]{#idx-kates-api}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [Where Results Live](02-architecture.md#where-results-live); [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Kind]{#idx-kind}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Prerequisites](20-installation-guide.md#prerequisites), [Installation](20-installation-guide.md#installation), [Network Policies](20-installation-guide.md#network-policies); [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [CI/CD Pipeline](appendix-c-cicd.md): Integration Tests (`integration.yml`); [Compliance Language Reference](appendix-compliance-language.md): [Values and Constraints](appendix-compliance-language.md#values-and-constraints)

[KRaft]{#idx-kraft}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Observability & Monitoring](09-observability.md): [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough), [The Dashboard Set](09-observability.md#the-dashboard-set); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation); [Kafka Deployment Engineering](15-kafka-deployment.md): [Cluster Architecture](15-kafka-deployment.md#cluster-architecture); [Migrating a Legacy Kafka Source](23-legacy-source-migration.md): [What the Source's Era Actually Changes](23-legacy-source-migration.md#what-the-sources-era-actually-changes); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[krafter]{#idx-krafter}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book), [Changing the Topology for Different Scenarios](03-cluster.md#changing-the-topology-for-different-scenarios); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), [Network Policies](20-installation-guide.md#network-policies), [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Network Policies](15-kafka-deployment.md#network-policies); [Compliance Checks](compliance.md): [When the Cluster Falls Short](compliance.md#when-the-cluster-falls-short); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies)

[Kyverno]{#idx-kyverno}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Prerequisites](20-installation-guide.md#prerequisites), [Environment Overlays](20-installation-guide.md#environment-overlays); [Deployment Guide](12-deployment.md): [Namespace Labels](12-deployment.md#namespace-labels); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security), [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [Upgrade Playbook](18-upgrade-playbook.md): [Kyverno Upgrade](18-upgrade-playbook.md#kyverno-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [CI/CD Pipeline](appendix-c-cicd.md): Backend CI (`ci.yml`)

## L

[Lab]{#idx-lab}
:   [Preface](index.qmd): [How to Read This Book](index.qmd#how-to-read-this-book), [Before You Start](index.qmd#before-you-start); [Test Types Deep Dive](05-test-types.md): [LOAD Test](05-test-types.md#load-test); [Lab — Interactive Performance Tuning](10b-lab.md): Lab — Interactive Performance Tuning, [When to Use the Lab](10b-lab.md#when-to-use-lab-vs-cli), [Layout](10b-lab.md#layout), [Iteration Workflow](10b-lab.md#iteration-workflow); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Leader Election]{#idx-leader-election}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Chaos Engineering Theory](06-chaos-theory.md): [Kafka-Specific Failure Modes](06-chaos-theory.md#kafka-specific-failure-modes)

[Library Chart]{#idx-library-chart}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation)

[Listener]{#idx-listener}
:   [The Cluster Under Test](03-cluster.md): [Listeners](03-cluster.md#listeners); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Kafka Listeners & Authentication, [Network Policies](20-installation-guide.md#network-policies); [Deployment Guide](12-deployment.md): [Cloud Deployment](12-deployment.md#cloud-deployment); [Security & Compliance](17-security.md): [Threat Model](17-security.md#threat-model), [Network Policies](17-security.md#network-policies); [Compliance Language Reference](appendix-compliance-language.md): [Kafka Settings](appendix-compliance-language.md#kafka-settings), [Lint Findings](appendix-compliance-language.md#lint-findings)

[LitmusChaos]{#idx-litmuschaos}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types); [Observability & Monitoring](09-observability.md): [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards); [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions), [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment)

[Log Truncation]{#idx-log-truncation}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Data Integrity Verification](08-data-integrity.md): [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results)

[Lost Record]{#idx-lost-record}
:   [Data Integrity Verification](08-data-integrity.md): [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results)

## M

[MCP]{#idx-mcp}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [CLI Reference](10-cli-reference.md): [Configuration](10-cli-reference.md#configuration), [Commands](10-cli-reference.md#commands)

[Metadata Log]{#idx-metadata-log}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book)

[Metadata Version]{#idx-metadata-version}
:   [Upgrade Playbook](18-upgrade-playbook.md): [Upgrade Strategy](18-upgrade-playbook.md#upgrade-strategy), [Two Versions, Two Steps](18-upgrade-playbook.md#two-versions-two-steps), [Kafka Version Upgrade](18-upgrade-playbook.md#kafka-version-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Metric Contract]{#idx-metric-contract}
:   [CI/CD Pipeline](appendix-c-cicd.md): MirrorMaker 2 CI (`ci-mirror-maker2.yml`)

[min.insync.replicas]{#idx-min.insync.replicas}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Data Integrity Verification](08-data-integrity.md): [Integrity Modes](08-data-integrity.md#integrity-modes), [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results), [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Compliance Checks](compliance.md): [Kafka Settings and Monitoring](compliance.md#kafka-settings-and-monitoring); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[MirrorMaker 2]{#idx-mirrormaker-2}
:   [Observability & Monitoring](09-observability.md): [The Dashboard Set](09-observability.md#the-dashboard-set); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Deployment Guide](12-deployment.md): [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Why Migration Is the Hard Case](22-mirror-maker2-migration.md#why-migration-is-the-hard-case), [Further Reading](22-mirror-maker2-migration.md#where-to-go-next); [Migrating a Legacy Kafka Source](23-legacy-source-migration.md): [What the Source's Era Actually Changes](23-legacy-source-migration.md#what-the-sources-era-actually-changes), [Where to Go Next](23-legacy-source-migration.md#where-to-go-next); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[mTLS]{#idx-mtls}
:   [Security & Compliance](17-security.md): [Authentication](17-security.md#authentication)

## N

[NetworkPolicy]{#idx-networkpolicy}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), Kafka Listeners & Authentication, [Network Policies](20-installation-guide.md#network-policies); [Kafka Deployment Engineering](15-kafka-deployment.md): [Network Policies](15-kafka-deployment.md#network-policies); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies); [Operating Kafka Connect](operating-kafka-connect.md): [Network Policies](operating-kafka-connect.md#network-policies), REST API & Connector Operations

[networkPolicyPeers]{#idx-networkpolicypeers}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Kafka Listeners & Authentication, [Network Policies](20-installation-guide.md#network-policies); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies); [Multi-Tenancy](19-multi-tenancy.md): [Multi-Tenancy Model](19-multi-tenancy.md#multi-tenancy-model)

[Node ID]{#idx-node-id}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types); [Deployment Guide](12-deployment.md): [Cloud Deployment](12-deployment.md#cloud-deployment)

[Noise Floor]{#idx-noise-floor}
:   [Performance Theory](04-performance-theory.md): [Telling a Regression from Noise](04-performance-theory.md#statistical-significance)

## O

[Offset]{#idx-offset}
:   [Architecture & Design](02-architecture.md): [Where Results Live](02-architecture.md#where-results-live); [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components); [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Why Migration Is the Hard Case](22-mirror-maker2-migration.md#why-migration-is-the-hard-case), [Testing a Migration Locally](22-mirror-maker2-migration.md#testing-a-migration-locally); [Migrating a Legacy Kafka Source](23-legacy-source-migration.md): [What MirrorMaker 2 Itself Requires](23-legacy-source-migration.md#what-mirrormaker-2-itself-requires); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Offset Translation]{#idx-offset-translation}
:   [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Testing a Migration Locally](22-mirror-maker2-migration.md#testing-a-migration-locally)

[Open-Loop Load]{#idx-open-loop-load}
:   [Performance Theory](04-performance-theory.md): [Coordinated Omission](04-performance-theory.md#coordinated-omission)

[Operator]{#idx-operator}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator), [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install), [Upgrading the Operator](deploying-strimzi-operator.md#upgrading-the-operator); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation); [Kafka Deployment Engineering](15-kafka-deployment.md): [Network Policies](15-kafka-deployment.md#network-policies), [Troubleshooting](15-kafka-deployment.md#troubleshooting); [Upgrade Playbook](18-upgrade-playbook.md): [Strimzi Operator Upgrade](18-upgrade-playbook.md#strimzi-operator-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[OTLP]{#idx-otlp}
:   [Observability & Monitoring](09-observability.md): [Distributed Tracing](09-observability.md#distributed-tracing)

## P

[Page Cache]{#idx-page-cache}
:   [The Cluster Under Test](03-cluster.md): The Cluster Under Test, [Resource Budget](03-cluster.md#resource-budget)

[panda]{#idx-panda}
:   [Preface](index.qmd): [Before You Start](index.qmd#before-you-start); [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology), [Operational Components](03-cluster.md#operational-components), [Changing the Topology for Different Scenarios](03-cluster.md#changing-the-topology-for-different-scenarios); [Performance Theory](04-performance-theory.md): [Why Latency Bends: Queues](04-performance-theory.md#why-latency-bends-queues); [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Kafka Deployment Engineering](15-kafka-deployment.md): [KafkaNodePool Configuration](15-kafka-deployment.md#kafkanodepool-configuration); [Compliance Checks](compliance.md): [Writing Constraints](compliance.md#writing-constraints)

[Partition]{#idx-partition}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book), [Physical Topology](03-cluster.md#physical-topology); [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Kafka Deployment Engineering](15-kafka-deployment.md): [Operational Components](15-kafka-deployment.md#operational-components); [Multi-Tenancy](19-multi-tenancy.md): [How Quotas Work](19-multi-tenancy.md#how-quotas-work); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Partition Leader]{#idx-partition-leader}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Safety Guardrails](07-chaos-practice.md#safety-guardrails)

[PDB]{#idx-pdb}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator)

[Percentile]{#idx-percentile}
:   [Performance Theory](04-performance-theory.md): Performance Theory, [Why Averages Lie](04-performance-theory.md#why-averages-lie); [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Lab — Interactive Performance Tuning](10b-lab.md): [Layout](10b-lab.md#layout); [Observability & Monitoring](09-observability.md): [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards), [The Meters Kates Publishes](09-observability.md#the-meters-kates-publishes); [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Performance Grade]{#idx-performance-grade}
:   [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[PersistentVolumeClaim]{#idx-persistentvolumeclaim}
:   See *PVC*.

[Plan]{#idx-plan}
:   See *disruption plan*.

[Platform Profile]{#idx-platform-profile}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), Topics & Users Reference, [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Topic Provisioning](15-kafka-deployment.md#topic-provisioning); [Security & Compliance](17-security.md): [Authorization](17-security.md#authorization), [Network Policies](17-security.md#network-policies)

[Playbook]{#idx-playbook}
:   [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Recipes & Patterns](14-recipes.md): [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [Troubleshooting Index](appendix-b-troubleshooting.md): [Upgrades](appendix-b-troubleshooting.md#upgrades)

[PodDisruptionBudget]{#idx-poddisruptionbudget}
:   See *PDB*.

[PolicyException]{#idx-policyexception}
:   [Security & Compliance](17-security.md): [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [Upgrade Playbook](18-upgrade-playbook.md): [Kyverno Upgrade](18-upgrade-playbook.md#kyverno-upgrade)

[PolicyReport]{#idx-policyreport}
:   [Security & Compliance](17-security.md): [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control)

[PostgreSQL]{#idx-postgresql}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [Where Results Live](02-architecture.md#where-results-live); [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions), [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [Security & Compliance](17-security.md): [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium); [Operating Kafka Connect](operating-kafka-connect.md): [Troubleshooting](operating-kafka-connect.md#troubleshooting); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[productionMode]{#idx-productionmode}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Helm Chart Structure](21-kafka-connect.md#helm-chart-structure)

[Profile]{#idx-profile}
:   [The Cluster Under Test](03-cluster.md): [Topics](03-cluster.md#topics); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), Topics & Users Reference, [Network Policies](20-installation-guide.md#network-policies); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies); [CLI Reference](10-cli-reference.md): [Configuration](10-cli-reference.md#configuration), [Commands](10-cli-reference.md#commands)

[Prometheus]{#idx-prometheus}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Chaos Engineering in Practice](07-chaos-practice.md): [SLA Grading](07-chaos-practice.md#sla-grading); [Observability & Monitoring](09-observability.md): [Observability Architecture](09-observability.md#observability-architecture), [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards), [Monitoring Stack Deployment](09-observability.md#monitoring-stack-deployment), [The Meters Kates Publishes](09-observability.md#the-meters-kates-publishes); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Network Policies](20-installation-guide.md#network-policies); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security)

[PrometheusRule]{#idx-prometheusrule}
:   [Observability & Monitoring](09-observability.md): [Alerting](09-observability.md#alerting); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), [Observability Stack](20-installation-guide.md#observability-stack); [Kafka Deployment Engineering](15-kafka-deployment.md): [Prometheus Alerts](15-kafka-deployment.md#prometheus-alerts); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Protobuf]{#idx-protobuf}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium), [Schema Registry Integration](21-kafka-connect.md#schema-registry-integration)

[Protocol Floor]{#idx-protocol-floor}
:   [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Why Migration Is the Hard Case](22-mirror-maker2-migration.md#why-migration-is-the-hard-case)

[Purgatory]{#idx-purgatory}
:   [Observability & Monitoring](09-observability.md): [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough)

[PVC]{#idx-pvc}
:   [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment)

## Q

[Quorum]{#idx-quorum}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book), [Replication Configuration](03-cluster.md#replication-configuration); [Observability & Monitoring](09-observability.md): [The Dashboard Set](09-observability.md#the-dashboard-set); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Kafka Deployment Engineering](15-kafka-deployment.md): [Cluster Architecture](15-kafka-deployment.md#cluster-architecture); [Compliance Checks](compliance.md): [Writing Constraints](compliance.md#writing-constraints), [Placing Pods Across Zones and Nodes](compliance.md#placing-pods-across-zones-and-nodes); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Quota]{#idx-quota}
:   [Kafka Deployment Engineering](15-kafka-deployment.md): [Broker Settings and Why](15-kafka-deployment.md#broker-settings-and-why); [Multi-Tenancy](19-multi-tenancy.md): [How Quotas Work](19-multi-tenancy.md#how-quotas-work), [Monitoring a Tenant](19-multi-tenancy.md#monitoring-a-tenant)

## R

[Rack]{#idx-rack}
:   [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology); [Observability & Monitoring](09-observability.md): [Broker Metrics Correlation](09-observability.md#broker-metrics-correlation); [Deployment Guide](12-deployment.md): [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [Compliance Checks](compliance.md): [Kafka Settings and Monitoring](compliance.md#kafka-settings-and-monitoring); [Compliance Language Reference](appendix-compliance-language.md): [Brokers and Controllers](appendix-compliance-language.md#brokers-and-controllers), [Kafka Settings](appendix-compliance-language.md#kafka-settings)

[readOnlyRootFilesystem]{#idx-readonlyrootfilesystem}
:   [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security)

[Rebalance]{#idx-rebalance}
:   [The Cluster Under Test](03-cluster.md): [Changing the Topology for Different Scenarios](03-cluster.md#changing-the-topology-for-different-scenarios); [Chaos Engineering Theory](06-chaos-theory.md): [Kafka-Specific Failure Modes](06-chaos-theory.md#kafka-specific-failure-modes); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Common Customizations](20-installation-guide.md#common-customizations), [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), [Optional Features](20-installation-guide.md#advanced-features), [Quick Reference](20-installation-guide.md#quick-reference); [Operating Kafka Connect](operating-kafka-connect.md): [Spreading Workers Across Zones](operating-kafka-connect.md#multi-az-deployment-strategy), [Observability](operating-kafka-connect.md#observability)

[Recipe]{#idx-recipe}
:   [Recipes & Patterns](14-recipes.md): Recipes & Patterns, [Recipe 1: Validate a Kafka Upgrade](14-recipes.md#recipe-1-validate-a-kafka-upgrade), [Recipe 2: Nightly Regression Suite](14-recipes.md#recipe-2-nightly-regression-suite), [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification), [Recipe 4: Investigate a Latency Regression](14-recipes.md#recipe-4-investigate-a-latency-regression), [Recipe 6: Producer Tuning](14-recipes.md#recipe-6-producer-tuning), [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka), [Recipe 8: Sign Off krafter for Payments](14-recipes.md#recipe-8-sign-off-krafter-for-payments)

[Reconciliation]{#idx-reconciliation}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [How the Operator Works](deploying-strimzi-operator.md#how-the-operator-works), [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator), [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Kafka Deployment Engineering](15-kafka-deployment.md): [Strimzi Operator](15-kafka-deployment.md#strimzi-operator); [Upgrade Playbook](18-upgrade-playbook.md): [Strimzi Operator Upgrade](18-upgrade-playbook.md#strimzi-operator-upgrade)

[Replication Factor]{#idx-replication-factor}
:   See *RF*.

[Replication Slot]{#idx-replication-slot}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium); [Operating Kafka Connect](operating-kafka-connect.md): [Troubleshooting](operating-kafka-connect.md#troubleshooting); [Troubleshooting Index](appendix-b-troubleshooting.md): [Kafka Connect](appendix-b-troubleshooting.md#kafka-connect)

[Request Handler Idle Ratio]{#idx-request-handler-idle-ratio}
:   [Observability & Monitoring](09-observability.md): [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough)

[Resilience Run]{#idx-resilience-run}
:   [Architecture & Design](02-architecture.md): [Disruption Engine](02-architecture.md#disruption-engine), [Where Results Live](02-architecture.md#where-results-live); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Safety Guardrails](07-chaos-practice.md#safety-guardrails); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Recipes & Patterns](14-recipes.md): [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Resilience Test]{#idx-resilience-test}
:   [Security & Compliance](17-security.md): [Securing Kates](17-security.md#securing-kates); [REST API Reference](11-api-reference.md): [Authentication](11-api-reference.md#authentication)

[RF]{#idx-rf}
:   [The Cluster Under Test](03-cluster.md): [Using the CLI to Inspect the Cluster](03-cluster.md#using-the-cli-to-inspect-the-cluster); [Chaos Engineering Theory](06-chaos-theory.md): [Key Metrics During Chaos](06-chaos-theory.md#key-metrics-during-chaos)

[RPO]{#idx-rpo}
:   [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Data Integrity Verification](08-data-integrity.md): [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results), [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Observability & Monitoring](09-observability.md): [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards), [Alerting](09-observability.md#alerting)

[RTO]{#idx-rto}
:   [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Data Integrity Verification](08-data-integrity.md): [Sequence Number Tracking](08-data-integrity.md#sequence-number-tracking), [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results); [Observability & Monitoring](09-observability.md): [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards), [Alerting](09-observability.md#alerting)

## S

[Safety Guard]{#idx-safety-guard}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints), [Error Responses](11-api-reference.md#error-responses)

[Saturation Point]{#idx-saturation-point}
:   [Performance Theory](04-performance-theory.md): Performance Theory, [The Two Pillars: Throughput and Latency](04-performance-theory.md#the-two-pillars-throughput-and-latency)

[Scenario File]{#idx-scenario-file}
:   [Scenario Files & SLA Gates](13-scenario-files.md): [Checking a File Before It Runs](13-scenario-files.md#checking-a-file-before-it-runs); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Recipes & Patterns](14-recipes.md): [Recipe 1: Validate a Kafka Upgrade](14-recipes.md#recipe-1-validate-a-kafka-upgrade), [Recipe 8: Sign Off krafter for Payments](14-recipes.md#recipe-8-sign-off-krafter-for-payments); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[SCRAM-SHA-512]{#idx-scram-sha-512}
:   [The Cluster Under Test](03-cluster.md): [Listeners](03-cluster.md#listeners); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Kafka Listeners & Authentication, Topics & Users Reference; [Compliance Checks](compliance.md): [Kafka Settings and Monitoring](compliance.md#kafka-settings-and-monitoring); [Security & Compliance](17-security.md): [Authentication](17-security.md#authentication), [Network Policies](17-security.md#network-policies); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [The KafkaConnect Custom Resource](21-kafka-connect.md#the-kafkaconnect-custom-resource)

[Secret]{#idx-secret}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Troubleshooting](15-kafka-deployment.md#troubleshooting); [Deployment Guide](12-deployment.md): [CLI Configuration](12-deployment.md#cli-configuration); [Security & Compliance](17-security.md): [Authentication](17-security.md#authentication); [Operating Kafka Connect](operating-kafka-connect.md): Security & Credential Rotation, [Troubleshooting](operating-kafka-connect.md#troubleshooting); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Authentication](11-api-reference.md#authentication)

[Security Grade]{#idx-security-grade}
:   [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[Share Group]{#idx-share-group}
:   Not used in a chapter yet.

[Single-Namespace Topology]{#idx-single-namespace-topology}
:   [Introduction](01-introduction.md): [Quick Start](01-introduction.md#quick-start); [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment)

[SLA]{#idx-sla}
:   [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Chaos Engineering in Practice](07-chaos-practice.md): [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks), [SLA Grading](07-chaos-practice.md#sla-grading); [Observability & Monitoring](09-observability.md): [Kates-Specific Dashboards](09-observability.md#kates-specific-dashboards); [Recipes & Patterns](14-recipes.md): [Recipe 3: Pre-Production Chaos Certification](14-recipes.md#recipe-3-pre-production-chaos-certification), [Recipe 7: Gate a CI Pipeline on Kafka](14-recipes.md#recipe-7-gate-a-ci-pipeline-on-kafka); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[SLA Grade]{#idx-sla-grade}
:   [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[SLI]{#idx-sli}
:   [CI/CD Pipeline](appendix-c-cicd.md): MirrorMaker 2 CI (`ci-mirror-maker2.yml`)

[SLO]{#idx-slo}
:   [Observability & Monitoring](09-observability.md): [Alerting](09-observability.md#alerting); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Observability Stack](20-installation-guide.md#observability-stack); [Operating Kafka Connect](operating-kafka-connect.md): [Observability](operating-kafka-connect.md#observability); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[SMT]{#idx-smt}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium), [Single Message Transforms (SMTs)](21-kafka-connect.md#single-message-transforms-smts)

[Snapshot]{#idx-snapshot}
:   [Observability & Monitoring](09-observability.md): [Latency Heatmaps](09-observability.md#latency-heatmaps); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Optional Features](20-installation-guide.md#advanced-features); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium); [Operating Kafka Connect](operating-kafka-connect.md): [Performance Tuning](operating-kafka-connect.md#performance-tuning), [Disaster Recovery](operating-kafka-connect.md#disaster-recovery); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands), [Exit Codes](10-cli-reference.md#exit-codes); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Sparkline]{#idx-sparkline}
:   [Observability & Monitoring](09-observability.md): [Trend Analysis](09-observability.md#trend-analysis); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[SSE]{#idx-sse}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Real-Time Monitoring](07-chaos-practice.md#real-time-monitoring)

[Steady-State Hypothesis]{#idx-steady-state-hypothesis}
:   [Chaos Engineering Theory](06-chaos-theory.md): Chaos Engineering Theory, [Core Principles](06-chaos-theory.md#core-principles)

[Strimzi]{#idx-strimzi}
:   [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [What the Chart Deploys](deploying-strimzi-operator.md#what-the-chart-deploys), [Deploying the Operator](deploying-strimzi-operator.md#deploying-the-operator); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation), [Network Policies](20-installation-guide.md#network-policies); [Security & Compliance](17-security.md): [Network Policies](17-security.md#network-policies), [Container Security](17-security.md#container-security); [Upgrade Playbook](18-upgrade-playbook.md): [Strimzi Operator Upgrade](18-upgrade-playbook.md#strimzi-operator-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands)

[StrimziPodSet]{#idx-strimzipodset}
:   [Chaos Engineering in Practice](07-chaos-practice.md): [Disruption Types](07-chaos-practice.md#disruption-types); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [How the Operator Works](deploying-strimzi-operator.md#how-the-operator-works)

[Super User]{#idx-super-user}
:   [Security & Compliance](17-security.md): [Authorization](17-security.md#authorization), [Securing Kates](17-security.md#securing-kates); [Troubleshooting Index](appendix-b-troubleshooting.md): [Quick Diagnostic Commands](appendix-b-troubleshooting.md#quick-diagnostic-commands)

## T

[Task]{#idx-task}
:   [Architecture & Design](02-architecture.md): [Where Results Live](02-architecture.md#where-results-live); [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [How Connect Divides the Work](21-kafka-connect.md#how-connect-divides-the-work); [Operating Kafka Connect](operating-kafka-connect.md): [Observability](operating-kafka-connect.md#observability), Security & Credential Rotation; [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [gRPC API Reference](16-grpc-api.md): [Message Types](16-grpc-api.md#message-types)

[Tenant]{#idx-tenant}
:   [Multi-Tenancy](19-multi-tenancy.md): Multi-Tenancy, [Multi-Tenancy Model](19-multi-tenancy.md#multi-tenancy-model), [Onboarding a New Service](19-multi-tenancy.md#onboarding-a-new-service), [How Quotas Work](19-multi-tenancy.md#how-quotas-work), [Monitoring a Tenant](19-multi-tenancy.md#monitoring-a-tenant), [Decommissioning a Tenant](19-multi-tenancy.md#decommissioning-a-tenant)

[Test Run]{#idx-test-run}
:   [Lab — Interactive Performance Tuning](10b-lab.md): [Iteration Workflow](10b-lab.md#iteration-workflow); [Chaos Engineering in Practice](07-chaos-practice.md): [Two Ways to Run a Fault](07-chaos-practice.md#two-ways-to-run-a-fault), [Resilience Runs: A Fault Inside a Test](07-chaos-practice.md#resilience-runs-a-fault-inside-a-test); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Security & Compliance](17-security.md): [Securing Kates](17-security.md#securing-kates); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Test Spec]{#idx-test-spec}
:   Not used in a chapter yet.

[Test Type]{#idx-test-type}
:   [Test Types Deep Dive](05-test-types.md): [Test Type Overview](05-test-types.md#test-type-overview); [Scenario Files & SLA Gates](13-scenario-files.md): [Spec Reference](13-scenario-files.md#spec-reference); [Lab — Interactive Performance Tuning](10b-lab.md): [Presets](10b-lab.md#presets), [Auto-Sweep Mode](10b-lab.md#auto-sweep-mode); [Deployment Guide](12-deployment.md): [Component-by-Component Deployment](12-deployment.md#component-by-component-deployment); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [gRPC API Reference](16-grpc-api.md): [Message Types](16-grpc-api.md#message-types)

[Throughput]{#idx-throughput}
:   [Performance Theory](04-performance-theory.md): [The Two Pillars: Throughput and Latency](04-performance-theory.md#the-two-pillars-throughput-and-latency); [Scenario Files & SLA Gates](13-scenario-files.md): [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates); [Lab — Interactive Performance Tuning](10b-lab.md): [Layout](10b-lab.md#layout); [Data Integrity Verification](08-data-integrity.md): [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Observability & Monitoring](09-observability.md): [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough), [The Meters Kates Publishes](09-observability.md#the-meters-kates-publishes); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints)

[Tiered Storage]{#idx-tiered-storage}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Verification](20-installation-guide.md#verification), [Environment Overlays](20-installation-guide.md#environment-overlays), [What the Chart Renders](20-installation-guide.md#chart-architecture-deep-dive), [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): [Troubleshooting](15-kafka-deployment.md#troubleshooting)

[Tombstone]{#idx-tombstone}
:   [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium)

[Topic]{#idx-topic}
:   [The Cluster Under Test](03-cluster.md): [Kafka You Need for This Book](03-cluster.md#kafka-you-need-for-this-book); [Scenario Files & SLA Gates](13-scenario-files.md): [Spec Reference](13-scenario-files.md#spec-reference); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): Topics & Users Reference; [Kafka Deployment Engineering](15-kafka-deployment.md): [Topic Provisioning](15-kafka-deployment.md#topic-provisioning); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [Compliance Language Reference](appendix-compliance-language.md): [Topics](appendix-compliance-language.md#topics)

[Topology]{#idx-topology}
:   [Introduction](01-introduction.md): [Quick Start](01-introduction.md#quick-start); [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology); [Deployment Guide](12-deployment.md): [Deployment Architecture Decisions](12-deployment.md#deployment-architecture-decisions), [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Compliance Language Reference](appendix-compliance-language.md): [Settings](appendix-compliance-language.md#settings), [Brokers and Controllers](appendix-compliance-language.md#brokers-and-controllers)

[Transactional Outbox]{#idx-transactional-outbox}
:   [Architecture & Design](02-architecture.md): [Where Results Live](02-architecture.md#where-results-live); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [CDC Patterns](21-kafka-connect.md#cdc-patterns)

[Trogdor]{#idx-trogdor}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture), [The Kates API](02-architecture.md#the-kates-api); [Test Types Deep Dive](05-test-types.md): [Two Ways to Generate Load](05-test-types.md#two-ways-to-generate-load); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [gRPC API Reference](16-grpc-api.md): [Message Types](16-grpc-api.md#message-types)

## U

[Unclean Leader Election]{#idx-unclean-leader-election}
:   [The Cluster Under Test](03-cluster.md): [Replication Configuration](03-cluster.md#replication-configuration); [Data Integrity Verification](08-data-integrity.md): [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results)

[Under-Replicated Partition]{#idx-under-replicated-partition}
:   [Chaos Engineering Theory](06-chaos-theory.md): [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology); [Deploying the Strimzi Operator](deploying-strimzi-operator.md): [Adopting an Operator Installed from the Upstream Chart](deploying-strimzi-operator.md#migrating-from-the-ad-hoc-install)

## V

[Values Overlay]{#idx-values-overlay}
:   [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Installation](20-installation-guide.md#installation); [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): [Why Migration Is the Hard Case](22-mirror-maker2-migration.md#why-migration-is-the-hard-case)

[Velero]{#idx-velero}
:   [Architecture & Design](02-architecture.md): [High-Level Architecture](02-architecture.md#high-level-architecture); [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): [Environment Overlays](20-installation-guide.md#environment-overlays), [Optional Features](20-installation-guide.md#advanced-features); [Kafka Deployment Engineering](15-kafka-deployment.md): Backup & Recovery; [Deployment Guide](12-deployment.md): [Makefile Reference](12-deployment.md#makefile-reference); [Security & Compliance](17-security.md): [Container Security](17-security.md#container-security); [Upgrade Playbook](18-upgrade-playbook.md): [Before and After Every Upgrade](18-upgrade-playbook.md#before-and-after-every-upgrade); [CI/CD Pipeline](appendix-c-cicd.md): Backend CI (`ci.yml`)

[Verdict]{#idx-verdict}
:   [Test Types Deep Dive](05-test-types.md): [INTEGRITY Test](05-test-types.md#integrity-test); [Chaos Engineering Theory](06-chaos-theory.md): [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology); [Data Integrity Verification](08-data-integrity.md): [Interpreting Integrity Results](08-data-integrity.md#interpreting-integrity-results), [Integrity Under Chaos](08-data-integrity.md#integrity-under-chaos); [Upgrade Playbook](18-upgrade-playbook.md): [Before and After Every Upgrade](18-upgrade-playbook.md#before-and-after-every-upgrade); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [REST API Reference](11-api-reference.md): [Endpoints](11-api-reference.md#endpoints); [Compliance Language Reference](appendix-compliance-language.md): [The Report](appendix-compliance-language.md#the-report)

[Virtual Thread]{#idx-virtual-thread}
:   Not used in a chapter yet.

## W

[Waiver]{#idx-waiver}
:   [Compliance Checks](compliance.md): [When the Cluster Falls Short](compliance.md#when-the-cluster-falls-short), [The Language at a Glance](compliance.md#the-language-at-a-glance); [CLI Reference](10-cli-reference.md): [Commands](10-cli-reference.md#commands); [Compliance Language Reference](appendix-compliance-language.md): [How to Read an Entry](appendix-compliance-language.md#how-to-read-an-entry), [Rule IDs](appendix-compliance-language.md#rule-ids), [Waivers](appendix-compliance-language.md#waivers), [The Report](appendix-compliance-language.md#the-report), [Lint Findings](appendix-compliance-language.md#lint-findings)

[Worker]{#idx-worker}
:   [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology); [Kafka Connect & CDC Pipelines](21-kafka-connect.md): [How Connect Divides the Work](21-kafka-connect.md#how-connect-divides-the-work), [The KafkaConnect Custom Resource](21-kafka-connect.md#the-kafkaconnect-custom-resource), [Exactly-Once Semantics](21-kafka-connect.md#exactly-once-semantics); [Operating Kafka Connect](operating-kafka-connect.md): [Spreading Workers Across Zones](operating-kafka-connect.md#multi-az-deployment-strategy), [Capacity Planning](operating-kafka-connect.md#capacity-planning), Security & Credential Rotation, [Upgrade Procedures](operating-kafka-connect.md#upgrade-procedures)

## Z

[Zone]{#idx-zone}
:   [The Cluster Under Test](03-cluster.md): [Physical Topology](03-cluster.md#physical-topology); [Chaos Engineering Theory](06-chaos-theory.md): [Core Principles](06-chaos-theory.md#core-principles); [Chaos Engineering in Practice](07-chaos-practice.md): [Built-In Playbooks](07-chaos-practice.md#built-in-playbooks); [Kafka Deployment Engineering](15-kafka-deployment.md): [KafkaNodePool Configuration](15-kafka-deployment.md#kafkanodepool-configuration); [Compliance Checks](compliance.md): [Placing Pods Across Zones and Nodes](compliance.md#placing-pods-across-zones-and-nodes), [Installing Kates for Compliance Checks](compliance.md#installing-for-compliance); [Operating Kafka Connect](operating-kafka-connect.md): [Spreading Workers Across Zones](operating-kafka-connect.md#multi-az-deployment-strategy); [Compliance Language Reference](appendix-compliance-language.md): [Brokers and Controllers](appendix-compliance-language.md#brokers-and-controllers)

:::
