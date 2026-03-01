package com.klster.kates.domain;

public record BulkDeleteResponse(
    int deleted,
    int notFound
) {}
