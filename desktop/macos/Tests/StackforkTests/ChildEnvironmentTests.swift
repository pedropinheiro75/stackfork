import Foundation
import Testing
@testable import Stackfork

@Suite struct ChildEnvironmentTests {
    let output = """
    Last login: today
    some profile banner=with equals
    __STACKFORK_ENV_BEGIN__
    PATH=/opt/homebrew/bin:/usr/bin
    DOCKER_HOST=unix:///x.sock
    NPM_TOKEN=secret
    LANG=en_US.UTF-8
    AWS_SECRET_ACCESS_KEY=secret
    SSH_AUTH_SOCK=/tmp/agent
    __STACKFORK_ENV_END__
    trailing noise
    """

    @Test func noiseBeforeTheMarkersIsIgnored() throws {
        let env = try #require(ChildEnvironment.parse(output))
        #expect(env["PATH"] == "/opt/homebrew/bin:/usr/bin")
        #expect(env["some profile banner"] == nil)
    }

    @Test func variablesOutsideTheAllowlistAreDropped() throws {
        let env = ChildEnvironment.filtered(try #require(ChildEnvironment.parse(output)))
        #expect(env["NPM_TOKEN"] == nil)
        #expect(env["AWS_SECRET_ACCESS_KEY"] == nil)
        #expect(Set(env.keys) == ["PATH", "DOCKER_HOST", "LANG", "SSH_AUTH_SOCK"])
        let merged = ChildEnvironment.merge(base: ["HOME": "/h", "PATH": "/usr/bin"], shell: ChildEnvironment.parse(output), home: "/h")
        #expect(merged.environment["NPM_TOKEN"] == nil)
        #expect(merged.environment["HOME"] == "/h")
        #expect(merged.environment["PATH"] == "/opt/homebrew/bin:/usr/bin")
        #expect(!merged.usedDefaultPath)
    }

    @Test func aMissingEndMarkerIsAFailure() {
        #expect(ChildEnvironment.parse("__STACKFORK_ENV_BEGIN__\nPATH=/x\n") == nil)
        #expect(ChildEnvironment.parse("PATH=/x\n") == nil)
    }

    @Test func fallbackPathAndFlag() {
        let merged = ChildEnvironment.merge(base: ["PATH": "/usr/bin:/bin"], shell: nil, home: "/Users/x")
        #expect(merged.usedDefaultPath)
        #expect(merged.environment["PATH"] == "/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin:/Users/x/.orbstack/bin:/Applications/Docker.app/Contents/Resources/bin")
    }

    @Test func aShellWithoutPathFallsBack() {
        let merged = ChildEnvironment.merge(base: ["PATH": "/usr/bin"], shell: ["LANG": "C"], home: "/h")
        #expect(merged.usedDefaultPath)
    }

    @Test func aShellThatPrintsTheMarkers() throws {
        let script = try makeScript("printf 'noise\\n__STACKFORK_ENV_BEGIN__\\nPATH=/from/shell\\nNPM_TOKEN=s\\n__STACKFORK_ENV_END__\\n'")
        let result = ChildEnvironment.resolve(base: ["PATH": "/usr/bin"], shell: script.path, home: "/h", timeout: 5)
        #expect(result.environment["PATH"] == "/from/shell")
        #expect(result.environment["NPM_TOKEN"] == nil)
        #expect(!result.usedDefaultPath)
    }

    @Test func aShellThatSleepsTenSecondsGivesTheFallbackWithinSix() throws {
        let script = try makeScript("sleep 10")
        let start = Date()
        let result = ChildEnvironment.resolve(base: ["PATH": "/usr/bin"], shell: script.path, home: "/h", timeout: 5)
        #expect(Date().timeIntervalSince(start) < 6)
        #expect(result.usedDefaultPath)
    }
}
