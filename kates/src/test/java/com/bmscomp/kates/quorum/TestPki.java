package com.bmscomp.kates.quorum;

import java.io.FileInputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.Key;
import java.security.KeyStore;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;

/** A CA, and member certificates it signs, made with the JDK's keytool, for quorum tests. */
final class TestPki {
    private static final String PASS = "changeit";
    private final Path dir;

    TestPki(Path dir) throws Exception {
        this.dir = Files.createDirectories(dir);
        keytool(
                "-genkeypair",
                "-alias",
                "ca",
                "-keyalg",
                "EC",
                "-groupname",
                "secp256r1",
                "-dname",
                "CN=ca",
                "-ext",
                "bc:c",
                "-validity",
                "2",
                "-keystore",
                "ca.p12");
        keytool("-exportcert", "-rfc", "-alias", "ca", "-keystore", "ca.p12", "-file", "ca.pem");
    }

    /** The member's certificate, key and the CA, as the three PEM files a member reads. */
    Path[] member(String name) throws Exception {
        String store = name + ".p12";
        keytool(
                "-genkeypair",
                "-alias",
                name,
                "-keyalg",
                "EC",
                "-groupname",
                "secp256r1",
                "-dname",
                "CN=" + name,
                "-validity",
                "2",
                "-keystore",
                store);
        keytool("-certreq", "-alias", name, "-keystore", store, "-file", name + ".csr");
        keytool(
                "-gencert",
                "-alias",
                "ca",
                "-keystore",
                "ca.p12",
                "-infile",
                name + ".csr",
                "-outfile",
                name + ".pem",
                "-rfc",
                "-validity",
                "2",
                "-ext",
                "san=dns:localhost,ip:127.0.0.1",
                "-ext",
                "eku=serverAuth,clientAuth");
        KeyStore keys = KeyStore.getInstance("PKCS12");
        try (FileInputStream in = new FileInputStream(dir.resolve(store).toFile())) {
            keys.load(in, PASS.toCharArray());
        }
        Key key = keys.getKey(name, PASS.toCharArray());
        Path pem = dir.resolve(name + ".key");
        Files.writeString(
                pem,
                "-----BEGIN PRIVATE KEY-----\n"
                        + Base64.getMimeEncoder(64, "\n".getBytes()).encodeToString(key.getEncoded())
                        + "\n-----END PRIVATE KEY-----\n");
        return new Path[] {dir.resolve(name + ".pem"), pem, dir.resolve("ca.pem")};
    }

    private void keytool(String... args) throws Exception {
        List<String> command = new ArrayList<>();
        command.add(Path.of(System.getProperty("java.home"), "bin", "keytool").toString());
        command.addAll(List.of(args));
        command.addAll(List.of("-storepass", PASS, "-keypass", PASS, "-storetype", "PKCS12", "-noprompt"));
        Process process = new ProcessBuilder(command)
                .directory(dir.toFile())
                .redirectErrorStream(true)
                .start();
        String output = new String(process.getInputStream().readAllBytes());
        if (process.waitFor() != 0) {
            throw new IllegalStateException(String.join(" ", args) + ": " + output);
        }
    }
}
