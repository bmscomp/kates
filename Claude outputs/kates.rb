class Kates < Formula
  desc "CLI for Kafka Advanced Testing & Engineering Suite"
  homepage "https://github.com/bmscomp/kates"
  version "1.22.0"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/bmscomp/kates/releases/download/v1.22.0/kates-darwin-arm64.tar.gz"
      sha256 "a5478ec02e71cba9ecaa0273c462c6a2925de37ab52d091d62e1b22bd119f9a8"
    else
      url "https://github.com/bmscomp/kates/releases/download/v1.22.0/kates-darwin-amd64.tar.gz"
      sha256 "71267a2d70a0932a8a8c221b63c3cc46e788306a19bed6c582d229003e2bdb29"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/bmscomp/kates/releases/download/v1.22.0/kates-linux-arm64.tar.gz"
      sha256 "615357bdb4f4926d45769150c1e1158fd9c58236eb134daf64cf7b455a86dadb"
    else
      url "https://github.com/bmscomp/kates/releases/download/v1.22.0/kates-linux-amd64.tar.gz"
      sha256 "13b33b97530d8201c3e384885ca03e204f8a48dbaf5eb96bed597621213befde"
    end
  end

  def install
    bin.install "kates"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kates version")
  end
end
