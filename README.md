# Slacked

GUI to export Slack threads into markdown.

> [!NOTE]
> Early stage, sorry for the terrible code.

This work is heavily relying on the amazing [`gh-slack`](https://github.com/rneatherway/gh-slack).
Go give it a :star:!

> [!IMPORTANT]
> On the first run, the app might ask you to unlock your macOS keychain (or
> equivalent in other OS), this is needed to access your Slack token and should
> be done only once.
> ![Image](https://github.com/nobe4/slacked/assets/2452791/a327acc0-7e79-419e-b703-f9c910a7f2c2)

## Build

> [!NOTE]
> Eventually, I'll setup an automated build and release system, but for now it's
> manual.

Build the binary yourself with:

```shell
go mod tidy
go build main.go
```

See [the official Fyne documentation](https://github.com/fyne-io/fyne) for troubleshooting.
