package com.klster.kates.webhook;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

import jakarta.enterprise.context.ApplicationScoped;

import org.jboss.logging.Logger;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.klster.kates.domain.TestRun;

/**
 * Sends HTTP POST notifications to registered webhook URLs
 * when test runs complete (DONE or FAILED).
 */
@ApplicationScoped
public class WebhookService {

    private static final Logger LOG = Logger.getLogger(WebhookService.class);
    private static final HttpClient HTTP = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5))
            .build();
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final List<WebhookRegistration> registrations = new CopyOnWriteArrayList<>();

    public void register(WebhookRegistration registration) {
        registrations.add(registration);
        LOG.infof("Registered webhook: %s → %s", registration.name(), registration.url());
    }

    public void unregister(String name) {
        registrations.removeIf(r -> r.name().equals(name));
        LOG.infof("Unregistered webhook: %s", name);
    }

    public List<WebhookRegistration> list() {
        return List.copyOf(registrations);
    }

    public void fireTestCompleted(TestRun run) {
        if (registrations.isEmpty()) {
            return;
        }

        var payload = new WebhookPayload(
                "test.completed",
                run.getId(),
                run.getTestType() != null ? run.getTestType().name() : "UNKNOWN",
                run.getStatus().name(),
                run.getCreatedAt()
        );

        for (WebhookRegistration reg : registrations) {
            fireAsync(reg, payload);
        }
    }

    private void fireAsync(WebhookRegistration reg, WebhookPayload payload) {
        Thread.startVirtualThread(() -> {
            try {
                String json = MAPPER.writeValueAsString(payload);
                HttpRequest request = HttpRequest.newBuilder()
                        .uri(URI.create(reg.url()))
                        .header("Content-Type", "application/json")
                        .header("X-Kates-Event", payload.event())
                        .POST(HttpRequest.BodyPublishers.ofString(json))
                        .timeout(Duration.ofSeconds(10))
                        .build();

                HttpResponse<String> response = HTTP.send(request, HttpResponse.BodyHandlers.ofString());
                if (response.statusCode() >= 200 && response.statusCode() < 300) {
                    LOG.debugf("Webhook %s delivered: %d", reg.name(), response.statusCode());
                } else {
                    LOG.warnf("Webhook %s returned %d: %s", reg.name(), response.statusCode(), response.body());
                }
            } catch (Exception e) {
                LOG.warnf("Webhook %s failed: %s", reg.name(), e.getMessage());
            }
        });
    }

    public record WebhookRegistration(String name, String url, String events) {}

    public record WebhookPayload(
            String event,
            String testId,
            String testType,
            String status,
            String timestamp
    ) {}
}
