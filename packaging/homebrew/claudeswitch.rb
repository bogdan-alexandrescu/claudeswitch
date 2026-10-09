# Homebrew formula for the claudeswitch CLI (IMPROVEMENTS F8).
#
# A template: version and the sha256 lines are filled in for each release by
# packaging/homebrew/update.sh, which the release workflow runs before it
# pushes this file to the tap's Formula/ folder. Each sha256 line ends with
# the target it belongs to; update.sh finds it by that comment.
class Claudeswitch < Formula
  desc "Keeps Claude Code on an account that still has quota"
  homepage "https://github.com/bogdan-alexandrescu/claudeswitch"
  version "0.0.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v#{version}/claudeswitch_v#{version}_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000" # darwin_arm64
    end
    on_intel do
      url "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v#{version}/claudeswitch_v#{version}_darwin_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000" # darwin_amd64
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v#{version}/claudeswitch_v#{version}_linux_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000" # linux_arm64
    end
    on_intel do
      url "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v#{version}/claudeswitch_v#{version}_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000" # linux_amd64
    end
  end

  def install
    bin.install "claudeswitch"
    # A link rather than an alias: it works in scripts and in Claude Code's
    # `!` prefix, as install.sh's does.
    bin.install_symlink "claudeswitch" => "cs"
  end

  def caveats
    <<~EOS
      Set it up (vault your accounts, write the config, install the daemon):
        claudeswitch setup

      The daemon service runs the binary it was installed from. After
      `brew upgrade claudeswitch`, point it at the new one:
        claudeswitch daemon install
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/claudeswitch version")
    assert_match version.to_s, shell_output("#{bin}/cs version")
  end
end
