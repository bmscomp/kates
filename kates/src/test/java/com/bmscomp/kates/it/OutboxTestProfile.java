package com.bmscomp.kates.it;

/**
 * Integration profile with the scheduled outbox poller switched OFF.
 *
 * <p>Row-count assertions ("one event per state change") are only meaningful if
 * nothing drains the table underneath them — the poller runs every two seconds
 * and would publish and delete rows mid-assertion. The tests drive
 * {@code processOutbox()} explicitly instead, which is also what makes the
 * publish→ack→delete ordering observable.
 *
 * <p>The overrides themselves live in {@link NoSchedulersTestProfile}, which
 * several read-side ITs share for the same reason.
 */
public class OutboxTestProfile extends NoSchedulersTestProfile {}
