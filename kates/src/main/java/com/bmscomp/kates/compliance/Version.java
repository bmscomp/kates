package com.bmscomp.kates.compliance;

import java.io.IOException;
import java.util.Arrays;
import java.util.List;
import java.util.Optional;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.DeserializationContext;
import com.fasterxml.jackson.databind.annotation.JsonDeserialize;
import com.fasterxml.jackson.databind.deser.std.StdDeserializer;

/**
 * A dotted version, compared part by part, where a missing part counts as 0:
 * 4.2 is 4.2.0. Records compare with {@link #compareTo}, not equals.
 */
@JsonDeserialize(using = Version.Reader.class)
public record Version(List<Integer> parts) implements Comparable<Version> {

    /** As a reference writes it: up to four numbers, with an optional leading v. */
    private static final Pattern DECLARED = Pattern.compile("v?(\\d{1,9}(?:\\.\\d{1,9}){0,3})");

    /** As a cluster reports it: the numbers, then any build suffix, as in v1.34.11+k3s1 or 4.2.0-rc1. */
    private static final Pattern REPORTED = Pattern.compile("v?(\\d{1,9}(?:\\.\\d{1,9})*)(?:[^.\\d].*)?");

    /**
     * The version a cluster reports, without its build suffix; empty when it
     * does not start with a version, so the caller can say it could not read
     * one rather than compare it.
     */
    public static Optional<Version> parse(String reported) {
        return reported == null ? Optional.empty() : of(REPORTED.matcher(reported.trim()));
    }

    private static Optional<Version> of(Matcher matcher) {
        if (!matcher.matches()) {
            return Optional.empty();
        }
        return Optional.of(new Version(Arrays.stream(matcher.group(1).split("\\."))
                .map(Integer::valueOf)
                .toList()));
    }

    @Override
    public int compareTo(Version other) {
        for (int i = 0; i < Math.max(parts.size(), other.parts.size()); i++) {
            int order = Integer.compare(part(i), other.part(i));
            if (order != 0) {
                return order;
            }
        }
        return 0;
    }

    private int part(int i) {
        return i < parts.size() ? parts.get(i) : 0;
    }

    @Override
    public String toString() {
        return parts.stream().map(String::valueOf).collect(Collectors.joining("."));
    }

    /**
     * Reads a declared version, which must be text: unquoted, YAML reads 4.10
     * as the number 4.1.
     */
    static final class Reader extends StdDeserializer<Version> {

        Reader() {
            super(Version.class);
        }

        @Override
        public Version deserialize(JsonParser p, DeserializationContext ctxt) throws IOException {
            if (p.currentToken().isNumeric()) {
                throw new Refusal(p, "quote the version (\"" + p.getText() + "\"): unquoted, it is a number");
            }
            if (p.currentToken() != JsonToken.VALUE_STRING) {
                throw new Refusal(p, "expected a version such as \"4.2.0\"");
            }
            return of(DECLARED.matcher(p.getText()))
                    .orElseThrow(() -> new Refusal(p, "expected a version such as \"4.2.0\""));
        }
    }
}
