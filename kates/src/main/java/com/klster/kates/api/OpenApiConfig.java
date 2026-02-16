package com.klster.kates.api;

import org.eclipse.microprofile.openapi.annotations.OpenAPIDefinition;
import org.eclipse.microprofile.openapi.annotations.info.Info;
import org.eclipse.microprofile.openapi.annotations.info.License;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

@OpenAPIDefinition(
        info = @Info(
                title = "Kates API",
                version = "1.0.0",
                description = "Kafka Advanced Testing & Engineering Suite — REST API for performance testing, "
                        + "chaos engineering, disruption testing, and observability of Apache Kafka clusters.",
                license = @License(name = "Apache 2.0", url = "https://www.apache.org/licenses/LICENSE-2.0")
        ),
        tags = {
                @Tag(name = "Tests", description = "Create, list, and manage performance test runs"),
                @Tag(name = "Cluster", description = "Inspect Kafka cluster topology, topics, consumer groups, and broker configs"),
                @Tag(name = "Health", description = "Application health and readiness"),
                @Tag(name = "Reports", description = "Generate, export, and compare test reports"),
                @Tag(name = "Disruptions", description = "Kubernetes-aware disruption and chaos testing"),
                @Tag(name = "Resilience", description = "Combined performance + chaos resilience testing"),
                @Tag(name = "Schedules", description = "Manage scheduled and recurring test configurations"),
                @Tag(name = "Trends", description = "Historical performance trend analysis")
        }
)
public class OpenApiConfig {
}
