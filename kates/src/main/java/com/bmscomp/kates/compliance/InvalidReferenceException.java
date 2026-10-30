package com.bmscomp.kates.compliance;

import com.bmscomp.kates.api.ApiError;

/**
 * Thrown when a body is not a reference the check can run: the error is the
 * response to send, a 400 with each field's reason keyed by its path, or a
 * 413 for a body over the limit.
 */
public class InvalidReferenceException extends RuntimeException {

    private final transient ApiError error;

    public InvalidReferenceException(ApiError error) {
        super(error.getMessage());
        this.error = error;
    }

    public ApiError getError() {
        return error;
    }
}
