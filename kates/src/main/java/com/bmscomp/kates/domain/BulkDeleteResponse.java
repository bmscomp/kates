package com.bmscomp.kates.domain;

public record BulkDeleteResponse(
    int deleted,
    int notFound
) {}
