package com.bmscomp.kates.compliance;

import java.io.IOException;
import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Objects;
import java.util.regex.Pattern;
import java.util.stream.Stream;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.BeanProperty;
import com.fasterxml.jackson.databind.DeserializationContext;
import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.JsonDeserializer;
import com.fasterxml.jackson.databind.JsonMappingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.annotation.JsonDeserialize;
import com.fasterxml.jackson.databind.deser.ContextualDeserializer;
import com.fasterxml.jackson.databind.deser.std.StdDeserializer;
import io.fabric8.kubernetes.api.model.Quantity;

/**
 * One declared value and how the live one is compared with it. A reference
 * writes it bare, meaning exact ({@code count: 3}), or as a map:
 * {@code {min: 3}}, {@code {min: "4.2.0", max: "4.3.9"}},
 * {@code {oneOf: [tls, scram-sha-512]}}, or {@code {equals: required,
 * severity: warn}} to add a severity.
 *
 * <p>equals and oneOf stand alone, min and max combine, and a map with none of
 * them is refused. T is the field's kind: Integer, a fabric8 Quantity, a
 * Version, a String for text and the schema's enums, a Boolean, or a JsonNode
 * scalar for a broker config.
 *
 * @param equalTo the reference's {@code equals}
 */
@JsonDeserialize(using = Constraint.Reader.class)
public record Constraint<T>(T equalTo, T min, T max, List<T> oneOf, Severity severity) {

    /** Every value the constraint names. */
    Stream<T> values() {
        return Stream.concat(Stream.of(equalTo, min, max), oneOf != null ? oneOf.stream() : Stream.empty())
                .filter(Objects::nonNull);
    }

    /**
     * Whether the observed value meets the constraint, with values ordered by
     * {@code order}: Comparator.naturalOrder() for every kind but a broker
     * config, so that 8Gi is more than 8000Mi and 4.2 equals 4.2.0.
     */
    public boolean holds(T observed, Comparator<? super T> order) {
        if (equalTo != null) {
            return order.compare(observed, equalTo) == 0;
        }
        if (oneOf != null) {
            return oneOf.stream().anyMatch(v -> order.compare(observed, v) == 0);
        }
        return (min == null || order.compare(observed, min) >= 0) && (max == null || order.compare(observed, max) <= 0);
    }

    /**
     * Whether a broker reports a value that meets a config constraint. min and
     * max compare numbers, and a value that is not one does not meet them.
     * equals and oneOf compare text, so false matches "false", and true and
     * false match in any case.
     */
    public static boolean configHolds(Constraint<JsonNode> constraint, String observed) {
        if (constraint.equalTo() != null) {
            return sameText(constraint.equalTo(), observed);
        }
        if (constraint.oneOf() != null) {
            return constraint.oneOf().stream().anyMatch(v -> sameText(v, observed));
        }
        if (observed == null) {
            return false;
        }
        BigDecimal value;
        try {
            value = new BigDecimal(observed.trim());
        } catch (NumberFormatException notANumber) {
            return false;
        }
        return (constraint.min() == null || value.compareTo(constraint.min().decimalValue()) >= 0)
                && (constraint.max() == null || value.compareTo(constraint.max().decimalValue()) <= 0);
    }

    private static boolean sameText(JsonNode expected, String observed) {
        String text = expected.asText();
        boolean bool = text.equalsIgnoreCase("true") || text.equalsIgnoreCase("false");
        return bool ? text.equalsIgnoreCase(observed) : text.equals(observed);
    }

    /** Reads the bare or the map form, with the field's kind as T. */
    static final class Reader extends StdDeserializer<Constraint<?>> implements ContextualDeserializer {

        private static final Pattern QUANTITY = Pattern.compile("\\d+(\\.\\d+)?(m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?");

        private final JavaType kind;

        Reader() {
            this(null);
        }

        private Reader(JavaType kind) {
            super(Constraint.class);
            this.kind = kind;
        }

        @Override
        public JsonDeserializer<?> createContextual(DeserializationContext ctxt, BeanProperty property) {
            return new Reader(ctxt.getContextualType().containedType(0));
        }

        /** An explicit null, as a YAML key with no value, would otherwise drop its rule without a word. */
        @Override
        public Constraint<?> getNullValue(DeserializationContext ctxt) throws JsonMappingException {
            throw new Refusal(ctxt.getParser(), "a value is required");
        }

        /** A field left out declares no rule. */
        @Override
        public Object getAbsentValue(DeserializationContext ctxt) {
            return null;
        }

        @Override
        public Constraint<?> deserialize(JsonParser p, DeserializationContext ctxt) throws IOException {
            if (!p.isExpectedStartObjectToken()) {
                return new Constraint<>(value(p, ctxt), null, null, null, Severity.FAIL);
            }
            Object equalTo = null;
            Object min = null;
            Object max = null;
            List<Object> oneOf = null;
            Severity severity = Severity.FAIL;
            for (String name = p.nextFieldName(); name != null; name = p.nextFieldName()) {
                p.nextToken();
                try {
                    if (p.currentToken() == JsonToken.VALUE_NULL) {
                        throw new Refusal(p, "a value is required");
                    }
                    switch (name) {
                        case "equals" -> equalTo = value(p, ctxt);
                        case "min" -> min = bound(p, ctxt);
                        case "max" -> max = bound(p, ctxt);
                        case "oneOf" -> oneOf = values(p, ctxt);
                        case "severity" -> severity = ctxt.readValue(p, Severity.class);
                        default -> throw new Refusal(p, "unknown field");
                    }
                } catch (JsonMappingException e) {
                    throw JsonMappingException.wrapWithPath(e, Constraint.class, name);
                }
            }
            if (equalTo == null && min == null && max == null && oneOf == null) {
                throw new Refusal(p, "needs equals, min, max or oneOf");
            }
            if (equalTo != null && (min != null || max != null || oneOf != null)) {
                throw new Refusal(p, "equals stands alone: drop min, max and oneOf");
            }
            if (oneOf != null && (min != null || max != null)) {
                throw new Refusal(p, "oneOf stands alone: drop min and max");
            }
            if (min != null && max != null && above(min, max)) {
                throw new Refusal(p, "min is above max, so nothing can meet it");
            }
            return new Constraint<>(equalTo, min, max, oneOf, severity);
        }

        @SuppressWarnings({"unchecked", "rawtypes"})
        private static boolean above(Object min, Object max) {
            if (min instanceof JsonNode low && max instanceof JsonNode high) {
                return low.decimalValue().compareTo(high.decimalValue()) > 0;
            }
            return ((Comparable) min).compareTo(max) > 0;
        }

        private Object value(JsonParser p, DeserializationContext ctxt) throws IOException {
            // Quantity's own deserializer takes any text, 8GB included, and the class annotation that names it
            // wins over a module's, so quantities are read here.
            Object value = kind.hasRawClass(Quantity.class) ? quantity(p) : ctxt.readValue(p, kind);
            if (value instanceof JsonNode node && !(node.isTextual() || node.isNumber() || node.isBoolean())) {
                throw new Refusal(p, "expected text, a number, or true or false");
            }
            return value;
        }

        /** As the JSON Schema writes a quantity: text such as 8Gi or 500m, or a number. */
        private static Quantity quantity(JsonParser p) throws IOException {
            // A number's own text: expanding 1e999999999 to digits would take a gigabyte.
            String text = p.currentToken().isNumeric() || p.currentToken() == JsonToken.VALUE_STRING ? p.getText() : "";
            if (!QUANTITY.matcher(text).matches()) {
                throw new Refusal(p, "expected a quantity such as 8Gi, 500m or 2");
            }
            return new Quantity(text);
        }

        private Object bound(JsonParser p, DeserializationContext ctxt) throws IOException {
            if (kind.hasRawClass(Boolean.class) || kind.hasRawClass(String.class)) {
                throw new Refusal(p, "min and max need a number, a quantity or a version");
            }
            if (kind.hasRawClass(JsonNode.class) && !p.currentToken().isNumeric()) {
                throw new Refusal(p, "a broker config's min and max are numbers");
            }
            return value(p, ctxt);
        }

        private List<Object> values(JsonParser p, DeserializationContext ctxt) throws IOException {
            if (kind.hasRawClass(Boolean.class)) {
                throw new Refusal(p, "true or false takes equals, not oneOf");
            }
            if (!p.isExpectedStartArrayToken()) {
                throw new Refusal(p, "expected a list");
            }
            List<Object> values = new ArrayList<>();
            while (p.nextToken() != JsonToken.END_ARRAY) {
                try {
                    if (p.currentToken() == JsonToken.VALUE_NULL) {
                        throw new Refusal(p, "a value is required");
                    }
                    values.add(value(p, ctxt));
                } catch (JsonMappingException e) {
                    throw JsonMappingException.wrapWithPath(e, values, values.size());
                }
            }
            if (values.isEmpty()) {
                throw new Refusal(p, "lists no value");
            }
            if (values.size() > KafkaReferenceReader.MAX_ONE_OF) {
                throw new Refusal(p, "lists more than " + KafkaReferenceReader.MAX_ONE_OF + " values");
            }
            return values;
        }
    }
}
