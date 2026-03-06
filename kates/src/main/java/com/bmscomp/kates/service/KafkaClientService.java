package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class KafkaClientService {

    private static final int TIMEOUT_SECONDS = 30;

    private final String bootstrapServers;

    @Inject
    public KafkaClientService(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    public Map<String, Object> produceRecord(String topic, String key, String value) {
        Properties props = new Properties();
        props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ProducerConfig.KEY_SERIALIZER_CLASS_CONFIG,
                "org.apache.kafka.common.serialization.StringSerializer");
        props.put(ProducerConfig.VALUE_SERIALIZER_CLASS_CONFIG,
                "org.apache.kafka.common.serialization.StringSerializer");
        props.put(ProducerConfig.ACKS_CONFIG, "all");
        props.put(ProducerConfig.REQUEST_TIMEOUT_MS_CONFIG, "15000");
        props.put(ProducerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");

        try (var producer = new KafkaProducer<String, String>(props)) {
            var record = new ProducerRecord<>(topic, key, value);
            var meta = producer.send(record).get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            Map<String, Object> result = new LinkedHashMap<>();
            result.put("topic", meta.topic());
            result.put("partition", meta.partition());
            result.put("offset", meta.offset());
            result.put("timestamp", meta.timestamp());
            return result;
        } catch (Exception e) {
            throw new RuntimeException("Failed to produce record to topic " + topic + ": " + e.getMessage(), e);
        }
    }

    public List<Map<String, Object>> fetchRecords(String topic, String offsetReset, int limit) {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG,
                "org.apache.kafka.common.serialization.StringDeserializer");
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG,
                "org.apache.kafka.common.serialization.StringDeserializer");
        props.put(ConsumerConfig.GROUP_ID_CONFIG, "kates-fetch-" + System.currentTimeMillis());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG,
                "earliest".equals(offsetReset) ? "earliest" : "latest");
        props.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "false");
        props.put(ConsumerConfig.MAX_POLL_RECORDS_CONFIG, limit);
        props.put(ConsumerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");

        List<Map<String, Object>> records = new ArrayList<>();
        try (var consumer = new KafkaConsumer<String, String>(props)) {
            consumer.subscribe(Collections.singleton(topic));
            int waited = 0;
            while (records.size() < limit && waited < 5000) {
                var polled = consumer.poll(java.time.Duration.ofMillis(500));
                waited += 500;
                for (var rec : polled) {
                    Map<String, Object> item = new LinkedHashMap<>();
                    item.put("offset", rec.offset());
                    item.put("partition", rec.partition());
                    item.put("timestamp", rec.timestamp());
                    item.put("key", rec.key());
                    item.put("value", rec.value());
                    records.add(item);
                    if (records.size() >= limit) break;
                }
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to consume from topic " + topic + ": " + e.getMessage(), e);
        }
        return records;
    }
}
