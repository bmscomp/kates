package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

/**
 * A Kubernetes label selector in the string form {@code kubectl -l} takes:
 * comma-separated requirements that must all hold, each one of
 * {@code key}, {@code !key}, {@code key=value} (or {@code ==}),
 * {@code key!=value}, {@code key in (a,b)} or {@code key notin (a,b)}.
 *
 * <p>{@code targetLabel} used to be split on its first {@code =} into one
 * key/value pair. {@code a=1,b=2} only worked because fabric8 glued the pair
 * back together into the query string, and {@code app in (a,b)} could not work
 * at all. Parsing up front rejects a malformed selector with a message instead
 * of an empty pod list, and lets the safety guard evaluate a selector against
 * pods it already holds.
 *
 * <p>An empty selector is rejected: Kubernetes reads it as "every object",
 * which for a fault means every pod in the namespace.
 */
public record ParsedLabelSelector(List<Requirement> requirements) {

    public enum Operator {
        EQUALS,
        NOT_EQUALS,
        IN,
        NOT_IN,
        EXISTS,
        DOES_NOT_EXIST
    }

    public record Requirement(String key, Operator operator, List<String> values) {

        public Requirement {
            values = List.copyOf(values);
        }

        boolean matches(Map<String, String> labels) {
            String actual = labels.get(key);
            return switch (operator) {
                case EQUALS -> actual != null && actual.equals(values.getFirst());
                case NOT_EQUALS -> actual == null || !actual.equals(values.getFirst());
                case IN -> actual != null && values.contains(actual);
                case NOT_IN -> actual == null || !values.contains(actual);
                case EXISTS -> actual != null;
                case DOES_NOT_EXIST -> actual == null;
            };
        }

        @Override
        public String toString() {
            return switch (operator) {
                case EQUALS -> key + "=" + values.getFirst();
                case NOT_EQUALS -> key + "!=" + values.getFirst();
                case IN -> key + " in (" + String.join(",", values) + ")";
                case NOT_IN -> key + " notin (" + String.join(",", values) + ")";
                case EXISTS -> key;
                case DOES_NOT_EXIST -> "!" + key;
            };
        }
    }

    // Label key: optional DNS-subdomain prefix, then a name of at most 63 chars.
    private static final Pattern NAME = Pattern.compile("[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?");
    private static final Pattern PREFIX =
            Pattern.compile("[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*");

    public ParsedLabelSelector {
        requirements = List.copyOf(requirements);
    }

    public static ParsedLabelSelector parse(String selector) {
        if (selector == null || selector.isBlank()) {
            throw new IllegalArgumentException("Empty label selector: it would match every pod in the namespace");
        }
        Parser p = new Parser(selector);
        List<Requirement> requirements = new ArrayList<>();
        do {
            requirements.add(p.requirement());
        } while (p.accept(","));
        if (!p.atEnd()) {
            throw p.error("expected ',' or end of selector");
        }
        return new ParsedLabelSelector(requirements);
    }

    /** True when every requirement holds for {@code labels} (null means no labels). */
    public boolean matches(Map<String, String> labels) {
        Map<String, String> l = labels != null ? labels : Map.of();
        return requirements.stream().allMatch(r -> r.matches(l));
    }

    /** The canonical string form, as sent to the API server. */
    @Override
    public String toString() {
        return requirements.stream().map(Requirement::toString).collect(Collectors.joining(","));
    }

    private static final class Parser {
        private final String src;
        private int pos;

        Parser(String src) {
            this.src = src;
        }

        Requirement requirement() {
            if (accept("!")) {
                return new Requirement(key(), Operator.DOES_NOT_EXIST, List.of());
            }
            String key = key();
            skipSpace();
            if (atEnd() || peek(",")) {
                return new Requirement(key, Operator.EXISTS, List.of());
            }
            if (accept("!=")) {
                return new Requirement(key, Operator.NOT_EQUALS, List.of(value()));
            }
            if (accept("==") || accept("=")) {
                return new Requirement(key, Operator.EQUALS, List.of(value()));
            }
            if (peek(">") || peek("<")) {
                throw error("'>' and '<' are not supported in pod selectors");
            }
            String op = word();
            Operator operator =
                    switch (op) {
                        case "in" -> Operator.IN;
                        case "notin" -> Operator.NOT_IN;
                        default -> throw error("expected =, ==, !=, in or notin after '" + key + "'");
                    };
            if (!accept("(")) {
                throw error("expected '(' after '" + op + "'");
            }
            List<String> values = new ArrayList<>();
            do {
                values.add(value());
            } while (accept(","));
            if (!accept(")")) {
                throw error("expected ',' or ')' in the value set of '" + key + "'");
            }
            if (values.stream().allMatch(String::isEmpty)) {
                throw error("the value set of '" + key + "' is empty");
            }
            return new Requirement(key, operator, values);
        }

        private String key() {
            String key = word();
            if (key.isEmpty()) {
                throw error("expected a label key");
            }
            int slash = key.indexOf('/');
            String prefix = slash >= 0 ? key.substring(0, slash) : null;
            String name = slash >= 0 ? key.substring(slash + 1) : key;
            boolean validPrefix = prefix == null
                    || (prefix.length() <= 253 && PREFIX.matcher(prefix).matches());
            if (!validPrefix || !NAME.matcher(name).matches()) {
                throw error("invalid label key '" + key + "'");
            }
            return key;
        }

        private String value() {
            String value = word();
            if (!value.isEmpty() && !NAME.matcher(value).matches()) {
                throw error("invalid label value '" + value + "'");
            }
            return value;
        }

        /** The next run of characters that are not whitespace or selector syntax. */
        private String word() {
            skipSpace();
            int start = pos;
            while (pos < src.length() && !isDelimiter(src.charAt(pos))) {
                pos++;
            }
            return src.substring(start, pos);
        }

        private static boolean isDelimiter(char c) {
            return Character.isWhitespace(c) || "!=(),<>".indexOf(c) >= 0;
        }

        boolean accept(String token) {
            skipSpace();
            if (src.startsWith(token, pos)) {
                pos += token.length();
                return true;
            }
            return false;
        }

        private boolean peek(String token) {
            skipSpace();
            return src.startsWith(token, pos);
        }

        boolean atEnd() {
            skipSpace();
            return pos >= src.length();
        }

        private void skipSpace() {
            while (pos < src.length() && Character.isWhitespace(src.charAt(pos))) {
                pos++;
            }
        }

        IllegalArgumentException error(String message) {
            return new IllegalArgumentException(
                    "Invalid label selector '" + src + "' at position " + pos + ": " + message);
        }
    }
}
