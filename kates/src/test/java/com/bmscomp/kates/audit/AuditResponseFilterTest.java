package com.bmscomp.kates.audit;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.mockStatic;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerResponseContext;
import jakarta.ws.rs.container.ResourceInfo;
import jakarta.ws.rs.core.UriInfo;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.MockedStatic;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.api.GlobalExceptionMapper;
import com.bmscomp.kates.service.AuditService;

class AuditResponseFilterTest {

    static class Resource {
        @Audited(action = "CREATE", type = "test")
        public void create() {}
    }

    private final AuditResponseFilter filter = new AuditResponseFilter();
    private final ContainerRequestContext request = mock(ContainerRequestContext.class);
    private final ContainerResponseContext response = mock(ContainerResponseContext.class);
    private MockedStatic<AuditService> blocking;

    @AfterEach
    void tearDown() {
        blocking.close();
    }

    @BeforeEach
    void setUp() throws NoSuchMethodException {
        // Outside Quarkus there is no worker pool: the write runs on the calling thread.
        blocking = mockStatic(AuditService.class);
        blocking.when(() -> AuditService.blockingSafe(any())).thenAnswer(invocation -> {
            invocation.<Runnable>getArgument(0).run();
            return null;
        });
        filter.resourceInfo = mock(ResourceInfo.class);
        when(filter.resourceInfo.getResourceMethod()).thenReturn(Resource.class.getMethod("create"));
        filter.trail = mock(AuditTrail.class);
        filter.actors = mock(Actors.class);
        when(filter.actors.current()).thenReturn(Actor.SCHEDULER);
        filter.auditService = mock(AuditService.class);
        UriInfo uri = mock(UriInfo.class);
        when(uri.getPath()).thenReturn("/api/tests");
        when(request.getUriInfo()).thenReturn(uri);
    }

    @Test
    void aDatabaseOutageAnswerIsNotHeldByItsAuditRow() {
        when(response.getStatus()).thenReturn(503);
        when(response.getEntity())
                .thenReturn(ApiError.of(503, "Service Unavailable", GlobalExceptionMapper.DATABASE_UNAVAILABLE));

        filter.filter(request, response);

        verify(filter.auditService, never()).record(anyString(), anyString(), anyString(), anyString(), any());
    }

    @Test
    void anotherRefusalIsStillAudited() {
        when(response.getStatus()).thenReturn(503);
        when(response.getEntity()).thenReturn(ApiError.of(503, "Service Unavailable", "draining"));

        filter.filter(request, response);

        verify(filter.auditService, times(1)).record(anyString(), anyString(), anyString(), anyString(), any());
    }
}
