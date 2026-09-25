package com.bmscomp.kates.api;

import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Standardized error response body for API errors.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class ApiError {

    private int status;
    private String error;
    private String message;
    private Map<String, String> fieldErrors;

    public ApiError() {}

    public ApiError(int status, String error, String message) {
        this.status = status;
        this.error = error;
        this.message = message;
    }

    public static ApiError of(int status, String error, String message) {
        return new ApiError(status, error, message);
    }

    /**
     * A 400 with the body a failed bean validation has, for a check made in
     * code: fieldErrors names each field, and the message names them too, for
     * clients that print only the message.
     */
    public static ApiError validationFailed(String message, Map<String, String> fieldErrors) {
        ApiError error = new ApiError(400, "Validation Failed", message);
        error.setFieldErrors(fieldErrors);
        return error;
    }

    public int getStatus() {
        return status;
    }

    public void setStatus(int status) {
        this.status = status;
    }

    public String getError() {
        return error;
    }

    public void setError(String error) {
        this.error = error;
    }

    public String getMessage() {
        return message;
    }

    public void setMessage(String message) {
        this.message = message;
    }

    public Map<String, String> getFieldErrors() {
        return fieldErrors;
    }

    public void setFieldErrors(Map<String, String> fieldErrors) {
        this.fieldErrors = fieldErrors;
    }
}
