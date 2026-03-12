class Linker < Formula
  desc "Local AI provider bridge for Claude Code"
  homepage "https://github.com/linker-cli/linker"
  url "https://github.com/linker-cli/linker/releases/download/v0.1.0/linker_darwin_arm64.tar.gz"
  sha256 "CHANGE_ME"
  license "MIT"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/linker-cli/linker/releases/download/v0.1.0/linker_darwin_amd64.tar.gz"
      sha256 "CHANGE_ME"
    end
    if Hardware::CPU.arm?
      url "https://github.com/linker-cli/linker/releases/download/v0.1.0/linker_darwin_arm64.tar.gz"
      sha256 "CHANGE_ME"
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/linker-cli/linker/releases/download/v0.1.0/linker_linux_amd64.tar.gz"
      sha256 "CHANGE_ME"
    end
    if Hardware::CPU.arm?
      url "https://github.com/linker-cli/linker/releases/download/v0.1.0/linker_linux_arm64.tar.gz"
      sha256 "CHANGE_ME"
    end
  end

  def install
    bin.install "linker"
  end

  service do
    run [opt_bin/"linker", "serve"]
    keep_alive true
    working_dir Dir.home
    log_path File.join(Dir.home, ".linker", "logs", "linker.log")
    error_log_path File.join(Dir.home, ".linker", "logs", "linker.log")
  end

  test do
    assert_match "0.1.0", shell_output("#{bin}/linker version")
  end
end
