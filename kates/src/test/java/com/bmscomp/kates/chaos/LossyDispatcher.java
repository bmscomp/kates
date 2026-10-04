package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;

import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import io.fabric8.kubernetes.client.server.mock.CustomResourceAware;
import io.fabric8.kubernetes.client.server.mock.KubernetesCrudDispatcher;
import io.fabric8.kubernetes.client.server.mock.Resetable;
import io.fabric8.mockwebserver.http.Dispatcher;
import io.fabric8.mockwebserver.http.MockResponse;
import io.fabric8.mockwebserver.http.RecordedRequest;

/**
 * The fabric8 CRUD mock API server, unreliable on demand. It can carry a
 * request out and answer it with a 500, as an API server does whose answer
 * never reaches the client, or answer with a 500 and carry nothing out. It
 * records every request that writes, for a test to count.
 */
public class LossyDispatcher extends Dispatcher implements Resetable, CustomResourceAware {

    private static final String SERVER_ERROR = "{\"apiVersion\":\"v1\",\"kind\":\"Status\",\"status\":\"Failure\","
            + "\"message\":\"the answer was lost\",\"reason\":\"InternalError\",\"code\":500}";

    private record Rule(String method, String pathPrefix, boolean carriedOut, AtomicBoolean spent) {
        boolean takes(RecordedRequest request) {
            return method.equals(request.getMethod())
                    && request.getPath().startsWith(pathPrefix)
                    && spent.compareAndSet(false, true);
        }
    }

    private final KubernetesCrudDispatcher crud = new KubernetesCrudDispatcher(List.of());
    private final List<Rule> rules = new CopyOnWriteArrayList<>();
    private final List<String> writes = new CopyOnWriteArrayList<>();

    /** The next {@code method} request whose path starts with {@code pathPrefix} is carried out, then answered 500. */
    public void loseAnswerOnce(String method, String pathPrefix) {
        rules.add(new Rule(method, pathPrefix, true, new AtomicBoolean()));
    }

    /** The next {@code method} request whose path starts with {@code pathPrefix} is answered 500, and not carried out. */
    public void failOnce(String method, String pathPrefix) {
        rules.add(new Rule(method, pathPrefix, false, new AtomicBoolean()));
    }

    /** Every request that wrote, or tried to, as "METHOD path", in the order they came. */
    public List<String> writes() {
        return List.copyOf(writes);
    }

    /** The writes whose path starts with {@code pathPrefix}, as "METHOD path". */
    public List<String> writes(String method, String pathPrefix) {
        List<String> matching = new ArrayList<>();
        for (String write : writes) {
            if (write.startsWith(method + " " + pathPrefix)) {
                matching.add(write);
            }
        }
        return matching;
    }

    @Override
    public MockResponse dispatch(RecordedRequest request) {
        if (!"GET".equals(request.getMethod())) {
            writes.add(request.getMethod() + " " + request.getPath());
        }
        for (Rule rule : rules) {
            if (rule.takes(request)) {
                if (rule.carriedOut()) {
                    crud.dispatch(request);
                }
                return new MockResponse().setResponseCode(500).setBody(SERVER_ERROR);
            }
        }
        return crud.dispatch(request);
    }

    /** Empties the store, and forgets the rules and the writes. */
    @Override
    public void reset() {
        crud.reset();
        rules.clear();
        writes.clear();
    }

    @Override
    public void expectCustomResource(CustomResourceDefinitionContext rdc) {
        crud.expectCustomResource(rdc);
    }
}
