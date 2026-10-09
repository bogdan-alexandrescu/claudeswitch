# Homebrew cask for the ClaudeSwitch menu-bar app (IMPROVEMENTS F8).
#
# A template: version and sha256 are filled in for each release by
# packaging/homebrew/update.sh, which the release workflow runs before it
# pushes this file to the tap's Casks/ folder.
cask "claudeswitch-app" do
  version "0.0.0"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"

  url "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v#{version}/ClaudeSwitch-#{version}-macos.zip"
  name "ClaudeSwitch"
  desc "Menu-bar app for claudeswitch, which keeps Claude Code on an account with quota"
  homepage "https://github.com/bogdan-alexandrescu/claudeswitch"

  depends_on macos: ">= :ventura"
  # The app drives the claudeswitch binary (0.5.1 or later).
  depends_on formula: "claudeswitch"

  app "ClaudeSwitch.app"

  zap trash: "~/Library/Preferences/xyz.claudeswitch.menubar.plist"

  caveats <<~EOS
    ClaudeSwitch is not signed with a Developer ID or notarized, so macOS
    blocks its first launch. Open it once, click Done, then go to
    System Settings > Privacy & Security and click Open Anyway.
    Or remove the quarantine flag yourself:
      xattr -dr com.apple.quarantine "#{appdir}/ClaudeSwitch.app"
  EOS
end
