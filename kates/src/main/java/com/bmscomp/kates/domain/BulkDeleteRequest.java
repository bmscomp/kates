package com.bmscomp.kates.domain;

import java.util.List;

public record BulkDeleteRequest(List<String> ids) {}
