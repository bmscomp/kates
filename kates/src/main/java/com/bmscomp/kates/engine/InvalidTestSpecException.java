package com.bmscomp.kates.engine;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * Thrown when a request sets a spec field the run it asks for could not honour:
 * a consumer setting for a type that starts no consumer, a rate for a type that
 * runs unthrottled, an option the producer's other settings rule out, a
 * scenario spec value outside its limits.
 *
 * <p>These used to be accepted and dropped, so the run went ahead on other
 * terms than the ones asked for and nothing said so. Refusing them names the
 * field instead. Distinct from {@link BenchmarkException} so the API can answer
 * with the same {@code fieldErrors} body as a failed bean validation.
 */
public class InvalidTestSpecException extends BenchmarkException {

    private final String prefix;
    private final Map<String, String> fieldErrors;

    /** Fields of a request's {@code spec}, keyed by field name. */
    public InvalidTestSpecException(Map<String, String> fieldErrors) {
        this("spec.", fieldErrors);
    }

    /**
     * Fields under {@code prefix} in the request, keyed by their path below it:
     * {@code spec.} and a field name for a plain request, {@code scenario.} and
     * {@code baseSpec.}-, {@code phases[i].spec.}- or {@code phases[i].}-prefixed
     * names, or {@code phases[i]} itself, for a scenario. A request with no type
     * has no prefix and the key {@code type}. The message spells out each full
     * path.
     */
    public InvalidTestSpecException(String prefix, Map<String, String> fieldErrors) {
        super(fieldErrors.entrySet().stream()
                .map(e -> prefix + e.getKey() + ": " + e.getValue())
                .collect(Collectors.joining("; ")));
        this.prefix = prefix;
        this.fieldErrors = new LinkedHashMap<>(fieldErrors);
    }

    /** Field path to reason, in the order the fields were checked. */
    public Map<String, String> getFieldErrors() {
        return fieldErrors;
    }

    /**
     * This refusal for a request sent as the field {@code field} of a larger
     * body, as a schedule's {@code testRequest} is: each field is keyed, and
     * named in the message, by its whole path in that body, such as
     * {@code testRequest.spec.consumerGroup}.
     */
    public InvalidTestSpecException under(String field) {
        Map<String, String> nested = new LinkedHashMap<>();
        fieldErrors.forEach((path, why) -> nested.put(field + "." + prefix + path, why));
        return new InvalidTestSpecException("", nested);
    }
}
