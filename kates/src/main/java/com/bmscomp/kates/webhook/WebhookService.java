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
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private volatile HttpClient httpClient;
    private final List<WebhookRegistration> registrations = new CopyOnWriteArrayList<>();

    private HttpClient http() {
        HttpClient client = httpClient;
        if (client == null) {
            synchronized (this) {
                client = httpClient;
                if (client == null) {
                    client = HttpClient.newBuilder()
                            .connectTimeout(Duration.ofSeconds(5))
                            .build();
                    httpClient = client;
                }
            }
        }
        return client;
    }

    public void register(WebhookRegistration registration) {
        registrations.add(registration);
        LOG.infof("Registered webhook: %s → %s", registration.name(), registration.url());
    }

    public void unregister(String name) {
        registrations.removeIf(r -> r.name().equals(name));
        LOG.infof("Unregistered webhook: %s", name);
    }

    @jakarta.inject.Inject
    jakarta.persistence.EntityManager em;

    @jakarta.inject.Inject
    WebhookService self;

    public List<WebhookRegistration> list() {
        return List.copyOf(registrations);
    }

    @jakarta.transaction.Transactional(jakarta.transaction.Transactional.TxType.REQUIRES_NEW)
    public boolean checkAndMarkProcessed(String idempotencyKey) {
        try {
            if (em.find(ProcessedEventEntity.class, idempotencyKey) != null) {
                return false;
            }
            em.persist(new ProcessedEventEntity(idempotencyKey));
            em.flush();
            return true;
        } catch (Exception e) {
            LOG.warn("Duplicate event detected (concurrent): " + idempotencyKey);
            return false;
        }
    }

    @org.eclipse.microprofile.reactive.messaging.Incoming("test-events-in")
    public void onTestEvent(com.bmscomp.kates.domain.events.TestEvent event) {
        String idempotencyKey = event.getTestId() + ":" + event.getStatus().name();
        if (!self.checkAndMarkProcessed(idempotencyKey)) {
            LOG.info("Skipping duplicate test event: " + idempotencyKey);
            return;
        }
        if (event.getStatus() != com.bmscomp.kates.domain.TestResult.TaskStatus.DONE && 
            event.getStatus() != com.bmscomp.kates.domain.TestResult.TaskStatus.FAILED) {
            return;
        }

        if (registrations.isEmpty()) {
            return;
        }

        var payload = new WebhookPayload(
                "test.completed",
                event.getTestId(),
                event.getTestType() != null ? event.getTestType() : "UNKNOWN",
                event.getStatus().name(),
                java.time.Instant.ofEpochMilli(event.getTimestamp()).toString());

        for (WebhookRegistration reg : registrations) {
            fireAsync(reg, payload);
        }
    }

    @jakarta.transaction.Transactional(jakarta.transaction.Transactional.TxType.REQUIRES_NEW)
    public void saveToDlq(WebhookRegistration reg, WebhookPayload payload, String errorMessage) {
        try {
            String jsonPayload = MAPPER.writeValueAsString(payload);
            WebhookDlqEntity dlqEntity = new WebhookDlqEntity(reg.name(), reg.url(), jsonPayload, errorMessage);
            em.persist(dlqEntity);
        } catch (Exception e) {
            LOG.error("Failed to persist DLQ event for webhook: " + reg.name(), e);
        }
    }

    private void fireAsync(WebhookRegistration reg, WebhookPayload payload) {
        Thread.startVirtualThread(() -> {
            int maxAttempts = 3;
            String lastError = "Unknown error";
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

                    HttpResponse<String> response = http().send(request, HttpResponse.BodyHandlers.ofString());
                    if (response.statusCode() >= 200 && response.statusCode() < 300) {
                        LOG.debugf("Webhook %s delivered (attempt %d): %d", reg.name(), attempt, response.statusCode());
                        return;
                    }
                    lastError = "HTTP " + response.statusCode();
                    LOG.warnf(
                            "Webhook %s returned %d (attempt %d/%d)",
                            reg.name(), response.statusCode(), attempt, maxAttempts);
                } catch (Exception e) {
                    lastError = e.getMessage();
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
                    "Webhook %s delivery failed after %d attempts for event %s. Sending to DLQ.",
                    reg.name(), maxAttempts, payload.event());
            self.saveToDlq(reg, payload, lastError);
        });
    }

    public record WebhookRegistration(String name, String url, String events) {
    }

    public record WebhookPayload(String event, String testId, String testType, String status, String timestamp) {
    }
}
