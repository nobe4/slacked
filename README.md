# Slacked ↧

GUI to export Slack threads into markdown.

![Icon.png](./Icon.png)

> [!NOTE]
> Early stage, sorry for the terrible code.

![Image](https://github.com/nobe4/slacked/assets/2452791/c11a282f-411f-4cca-a79d-b80c5cb7c6d7)

This work relies heavily on the amazing [`gh-slack`](https://github.com/rneatherway/gh-slack).
Go give it a :star:!

## Install

Download the [latest release](https://github.com/nobe4/slacked/releases/latest).

> [!IMPORTANT]
> The app is currently not signed, I will need to work on that.
> - on macOS you might need to run: `xattr -r -d com.apple.quarantine path/to/slacked.app`
> - on windows you might need to run something? (I don't have a windows machine)

## Use

- select a message from slack
- copy its link: of the format `https://<org>.slack.com/archives/<id>/<id>`
- paste it into the input field with the placeholder `slack url`
- click `archive`
- click `copy`

> [!IMPORTANT]
> On the first run, the app might ask you to unlock your macOS keychain (or
> equivalent in other OS), this is needed to access your Slack token and should
> be done only once.
> ![Image](https://github.com/nobe4/slacked/assets/2452791/a327acc0-7e79-419e-b703-f9c910a7f2c2)

## Build

Build the binary yourself with:

```shell
go mod tidy
go build main.go
```

See [the official Fyne documentation](https://github.com/fyne-io/fyne) for troubleshooting.
