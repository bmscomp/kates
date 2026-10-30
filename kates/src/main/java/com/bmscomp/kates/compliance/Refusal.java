package com.bmscomp.kates.compliance;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.databind.JsonMappingException;

/**
 * A value the reference's own readers refuse, with the reason to show as it
 * is. Jackson adds the path as the exception leaves each enclosing value.
 */
final class Refusal extends JsonMappingException {

    Refusal(JsonParser parser, String reason) {
        super(parser, reason);
    }
}
