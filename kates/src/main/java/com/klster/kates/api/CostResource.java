package com.klster.kates.api;

import java.util.LinkedHashMap;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.klster.kates.service.KafkaAdminService;

/**
 * Server-side cost estimation using real cluster metadata.
 */
@Path("/api/cost")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Cost")
public class CostResource {

    @Inject
    KafkaAdminService kafkaAdmin;

    record CostRequest(String cloud, int records, int recordSize, int durationSeconds, int brokers, int replicas) {

        CostRequest {
            if (cloud == null || cloud.isEmpty()) cloud = "aws";
            if (records <= 0) records = 100000;
            if (recordSize <= 0) recordSize = 512;
            if (durationSeconds <= 0) durationSeconds = 300;
            if (brokers <= 0) brokers = 3;
            if (replicas <= 0) replicas = 3;
        }
    }

    record CostModel(String name, double storagePerGB, double networkIn, double networkOut, double brokerHourly) {}

    private static final Map<String, CostModel> MODELS = Map.of(
            "aws", new CostModel("AWS MSK (us-east-1)", 0.10, 0.00, 0.09, 0.48),
            "azure", new CostModel("Azure Event Hubs (East US)", 0.045, 0.00, 0.087, 0.52),
            "gcp", new CostModel("GCP Pub/Sub (us-central1)", 0.04, 0.00, 0.12, 0.45),
            "confluent", new CostModel("Confluent Cloud", 0.10, 0.00, 0.11, 1.20));

    @POST
    @Path("/estimate")
    @Consumes(MediaType.APPLICATION_JSON)
    @Operation(
            summary = "Estimate cloud cost",
            description = "Calculates estimated cost based on test params and cloud provider pricing")
    public Response estimate(CostRequest req) {
        CostModel model = MODELS.get(req.cloud().toLowerCase());
        if (model == null) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity(ApiError.of(
                            400,
                            "Bad Request",
                            "Unknown cloud provider: " + req.cloud() + ". Use: aws, azure, gcp, confluent"))
                    .build();
        }

        int actualBrokers = req.brokers();
        int clusterBrokers = kafkaAdmin.brokerCount();
        if (clusterBrokers > 0) {
            actualBrokers = clusterBrokers;
        }

        double dataGB = (double) req.records() * req.recordSize() / (1024.0 * 1024 * 1024);
        double storageGB = dataGB * req.replicas();
        double networkInGB = dataGB;
        double networkOutGB = dataGB * (req.replicas() - 1);
        double durationHours = Math.max(req.durationSeconds() / 3600.0, 1.0 / 3600);

        double storageCost = storageGB * model.storagePerGB();
        double networkInCost = networkInGB * model.networkIn();
        double networkOutCost = networkOutGB * model.networkOut();
        double brokerCost = actualBrokers * durationHours * model.brokerHourly();
        double total = storageCost + networkInCost + networkOutCost + brokerCost;

        Map<String, Object> result = new LinkedHashMap<>();
        result.put("provider", model.name());
        result.put("clusterBrokers", clusterBrokers > 0 ? clusterBrokers : "unknown (using estimate)");

        Map<String, Object> breakdown = new LinkedHashMap<>();
        breakdown.put("storageGB", round(storageGB));
        breakdown.put("storageCost", round(storageCost));
        breakdown.put("networkInGB", round(networkInGB));
        breakdown.put("networkInCost", round(networkInCost));
        breakdown.put("networkOutGB", round(networkOutGB));
        breakdown.put("networkOutCost", round(networkOutCost));
        breakdown.put("brokerHours", round(actualBrokers * durationHours));
        breakdown.put("brokerCost", round(brokerCost));
        result.put("breakdown", breakdown);
        result.put("totalCost", round(total));

        return Response.ok(result).build();
    }

    private static double round(double v) {
        return Math.round(v * 100.0) / 100.0;
    }
}
