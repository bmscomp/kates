package com.bmscomp.kates.security;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Optional;

import org.junit.jupiter.api.Test;

class ApiKeysTest {

    private static ApiKeys keys(String legacy) {
        ApiKeys keys = new ApiKeys();
        keys.securityEnabled = true;
        keys.legacyKey = Optional.ofNullable(legacy);
        return keys;
    }

    @Test
    void theLegacyKeyIsAHumanWithEveryScope() {
        KatesPrincipal p = keys("s3cret").resolve("s3cret").orElseThrow();
        assertEquals(ApiKeys.LEGACY, p.name());
        assertEquals(KatesPrincipal.Type.HUMAN, p.type());
        assertEquals(Scopes.ALL, p.scopes());
        assertTrue(p.allowedClusterIds().isEmpty());
    }

    @Test
    void noOtherKeyResolves() {
        assertTrue(keys("s3cret").resolve("s3cre").isEmpty());
        assertTrue(keys("s3cret").resolve("s3cret ").isEmpty());
        assertTrue(keys("s3cret").resolve("").isEmpty());
        assertTrue(keys("s3cret").resolve(null).isEmpty());
        // No key configured: nothing resolves, not even an empty key.
        assertTrue(keys(null).resolve("").isEmpty());
        assertTrue(keys("").resolve("").isEmpty());
    }

    @Test
    void aKeyIsReadFromBearerFirstThenXApiKey() {
        assertEquals("abc", ApiKeys.presented("Bearer abc ", "xyz"));
        assertEquals("xyz", ApiKeys.presented(null, " xyz "));
        assertEquals("xyz", ApiKeys.presented("Basic abc", "xyz"));
        assertNull(ApiKeys.presented(null, " "));
        assertNull(ApiKeys.presented("Basic abc", null));
    }
}
