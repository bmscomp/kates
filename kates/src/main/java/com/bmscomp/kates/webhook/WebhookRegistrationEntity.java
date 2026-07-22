package com.bmscomp.kates.webhook;

import java.time.Instant;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "webhook_registrations")
public class WebhookRegistrationEntity {

    @Id
    @Column(name = "name", length = 255)
    private String name;

    @Column(name = "url", length = 2048, nullable = false)
    private String url;

    @Column(name = "events")
    private String events;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    public WebhookRegistrationEntity() {}

    public WebhookRegistrationEntity(String name, String url, String events) {
        this.name = name;
        this.url = url;
        this.events = events;
        this.createdAt = Instant.now();
    }

    public String getName() {
        return name;
    }

    public String getUrl() {
        return url;
    }

    public String getEvents() {
        return events;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public void setUrl(String url) {
        this.url = url;
    }

    public void setEvents(String events) {
        this.events = events;
    }
}
