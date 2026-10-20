package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.stream.Stream;

import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

import com.bmscomp.kates.api.ApiError;

/**
 * The corpus kates conformity lint is held to (cli/internal/conformity/
 * testdata): each file in invalid/ names, on its "# expect:" line, the path
 * the reader refuses. The CLI's TestCompilerRefusesWhatTheReaderRefuses
 * needs an error at the same path, so the offline compiler and this reader
 * refuse the same references.
 */
class KafkaReferenceCorpusTest {

    private static final Path INVALID = Path.of("..", "cli", "internal", "conformity", "testdata", "invalid");
    private static final Pattern EXPECT = Pattern.compile("(?m)^# expect: (\\S+)");

    static Stream<String> invalid() throws IOException {
        try (Stream<Path> files = Files.list(INVALID)) {
            return files
                    .map(f -> f.getFileName().toString())
                    .filter(name -> name.endsWith(".yaml"))
                    .sorted()
                    .toList()
                    .stream();
        }
    }

    @ParameterizedTest(name = "{0}")
    @MethodSource("invalid")
    void refusesEachInvalidReferenceAtItsPath(String file) throws IOException {
        String yaml = Files.readString(INVALID.resolve(file));
        Matcher expect = EXPECT.matcher(yaml);
        assertTrue(expect.find(), "no # expect: line");

        ApiError error = assertThrows(
                        InvalidReferenceException.class, () -> new KafkaReferenceReader().read(json(yaml)))
                .getError();

        assertTrue(
                error.getFieldErrors().containsKey(expect.group(1)),
                () -> "refused, but not at " + expect.group(1) + ": " + error.getFieldErrors());
    }
}
