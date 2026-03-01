package com.bmscomp.kates.webhook;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import jakarta.enterprise.context.ApplicationScoped;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.TestRun;

/**
 * Sends HTTP POST notifications to registered webhook URLs
 * when test runs complete (DONE or FAILED).
 */
@ApplicationScoped
public class WebhookService {

    private static final Logger LOG = Logger.getLogger(WebhookService.class);
    private static final HttpClient HTTP =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
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
                run.getCreatedAt());

        for (WebhookRegistration reg : registrations) {
            fireAsync(reg, payload);
        }
    }

    private void fireAsync(WebhookRegistration reg, WebhookPayload payload) {
        Thread.startVirtualThread(() -> {
            int maxAttempts = 3;
            for (int attempt = 1; attempt <= maxAttempts; attempt++) {
                try {
                    String json = MAPPER.writeValueAsString(payload);
                    HttpRequest request = HttpRequest.newBuilder()
                            .uri(URI.create(reg.url()))
                            .header("Content-Type", "application/json")
                            .header("X-Kates-Event", payload.event())
                            .header("X-Kates-Attempt", String.valueOf(attempt))
                            .POST(HttpRequest.BodyPublishers.ofString(json))
                            .timeout(Duration.ofSeconds(10))
                            .build();

                    HttpResponse<String> response = HTTP.send(request, HttpResponse.BodyHandlers.ofString());
                    if (response.statusCode() >= 200 && response.statusCode() < 300) {
                        LOG.debugf("Webhook %s delivered (attempt %d): %d", reg.name(), attempt, response.statusCode());
                        return;
                    }
                    LOG.warnf(
                            "Webhook %s returned %d (attempt %d/%d)",
                            reg.name(), response.statusCode(), attempt, maxAttempts);
                } catch (Exception e) {
                    LOG.warnf(
                            "Webhook %s failed (attempt %d/%d): %s", reg.name(), attempt, maxAttempts, e.getMessage());
                }

                if (attempt < maxAttempts) {
                    try {
                        Thread.sleep(1000L * (1L << (attempt - 1)));
                    } catch (InterruptedException ie) {
                        Thread.currentThread().interrupt();
                        return;
                    }
                }
            }
            LOG.errorf(
                    "Webhook %s delivery failed after %d attempts for event %s",
                    reg.name(), maxAttempts, payload.event());
        });
    }

    public record WebhookRegistration(String name, String url, String events) {}

    public record WebhookPayload(String event, String testId, String testType, String status, String timestamp) {}
}
