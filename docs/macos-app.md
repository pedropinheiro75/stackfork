# Stackfork for macOS

`Stackfork.app` is a menu bar app for macOS 14 or later. It runs its own dashboard, lists your instances in the menu bar with **Start** and **Stop**, and opens the dashboard in a window. It needs no installed CLI. Press Control-Option-Command-S (`⌃⌥⌘S`) anywhere to show the window.

## Build the app

Until the first public release, build the DMG on your Mac. You need Xcode and Go.

1. From the root of a clone, run `scripts/build-macos-app.sh 0.2.0`. The script writes `dist/macos-app/stackfork-app_0.2.0_darwin_universal.dmg` and a `.sha256` file.
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
