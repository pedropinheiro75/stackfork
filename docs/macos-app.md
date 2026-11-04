# Stackfork for macOS

`Stackfork.app` is a menu bar app for macOS 14 or later. It runs its own dashboard, lists your instances in the menu bar with **Start** and **Stop**, and opens the dashboard in a window. It needs no installed CLI. Press Control-Option-Command-S (`⌃⌥⌘S`) anywhere to show the window.

## Install the app

1. Download `stackfork-app_<version>_darwin_universal.dmg` from the [Releases page](https://github.com/pedropinheiro75/stackfork/releases). To build it yourself instead, run `scripts/build-macos-app.sh <version>` from the root of a clone (Xcode and Go needed); it writes the DMG and a `.sha256` file to `dist/macos-app/`.
2. Open the DMG and drag `Stackfork.app` to Applications.
3. If macOS refuses to open the app, clear the quarantine attribute:

   ```sh
   xattr -dr com.apple.quarantine /Applications/Stackfork.app
   ```

4. Open Stackfork. The first menu row reads `Dashboard running`.

The app is signed ad hoc and is not notarized.

## Run it with the CLI

Choose **Open at Login** in the menu to start the app after you log in. This works only when the app is in `/Applications` or `~/Applications`.

The app and a `stackfork dashboard` started in a terminal can run side by side. They share the same state, and only one of them watches your instances at a time.
