package com.klster.kates.domain;

import java.util.List;

public record BulkDeleteRequest(List<String> ids) {}
