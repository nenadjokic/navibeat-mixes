# `nbui1`: how the plugin tells every NaviBeat client to draw its mixes

**Status: BOTH halves are built.** The client reads these lines as of its next release; this plugin
writes them as of **0.7.0**. The document stays as the contract, because the Kotlin client family
still has to implement its side and both parsers were written from here.

Apple-side implementation: `NaviBeatCore/Sources/NaviBeatModels/MixPresentation.swift`,
tests in `NaviBeatCore/Tests/NaviBeatModelsTests/MixPresentationTests.swift`.

---

## Why this exists

Nenad's rule, and it is the reason the setting is here rather than in the app:

> If we have settings inside of the NaviBeat application and some person installed NaviBeat but
> doesn't have the NaviBeat Mixes plugin enabled in Navidrome, then what was the point? It'll be
> some kind of a stub, and people will just be confused with this option.

That is not hypothetical. `MixCoverSettingsToggle` sits unconditionally in Settings > Appearance on
iOS, macOS and tvOS today, and on a server with no mixes it flips a preference that can never apply.
Meanwhile the Home shelf for mixes is already existence-gated. So presentation config belongs with
the thing that creates the feature: the plugin.

---

## The one rule that must not be broken

**Never touch the `nb1:` line, and never add a field to it.**

`MixDescriptor` requires **exactly six** colon-separated fields and the literal `nb1:` prefix:

```
nb1:timeofday:morning:2026-07-28:affinity:30
```

A seventh field, or a bump to `nb2:`, makes **every installed NaviBeat return nil**. The playlist
then stops being recognised as a mix: it drops off the NaviBeat Mixes shelf, loses its generated
cover, loses its hour-of-day ordering, and its machine line becomes visible text in the description.
That is a total feature outage for everyone who has not updated.

Presentation therefore travels on its **own line, with its own version prefix**, which older clients
simply do not recognise.

---

## The lines

### 1. Server-wide style, on the CONTROL playlist

```
nbui1:style:cover
nbui1:style:button
```

Written into the **control playlist's** description, as its own line. The control playlist is the
right carrier for two reasons that already hold today:

- clients find it by machine line (`kind == .control`), never by name, because its name carries a
  user-configurable prefix;
- clients **exclude it from the shelf**, so no client ever renders its description, which means an
  old client cannot show this line as stray text.

`MixControlMailbox.commentWriting` (client side) preserves every line it does not recognise byte for
byte, so the existing re-roll mailbox and this line coexist without either clobbering the other.

**Absence is meaningful.** A client that finds no `nbui1:style:` line keeps drawing exactly what it
drew before. Do not treat "no line" as `cover`; write the line explicitly when the user chooses.

### 2. Per-playlist button, on EACH mix playlist

```
nbui1:btn:<glyph>:<hex>:<label>
```

Example, as the fourth line of the description:

```
NaviBeat Mixes: your morning, built from what you actually play at this time of day.
Made by NaviBeat  ·  navibeat.app
nb1:timeofday:morning:2026-07-28:affinity:30
nbui1:btn:sunrise:F2A65A:Morning Mix
```

| Field | Rule |
|---|---|
| `glyph` | one of the vocabulary below, lowercase. An unknown name does NOT reject the line; the client falls back to a generic icon. |
| `hex` | **exactly six hex digits, no `#`**. Anything else rejects the WHOLE line and the client uses its own default for that mix. Strict on purpose: a half-read colour renders as an arbitrary shade with nothing to tell the user it was wrong. |
| `label` | everything after the fourth colon, so it **may contain colons** ("Rock: the loud half"). May be empty, in which case the client uses the playlist's own name. Must be the last field. |

**The plugin writes the button line ONLY when the style is `button`.** That is not an optimisation,
it is what keeps the rollout safe: a NaviBeat old enough not to know the `nbui1` namespace renders any
line it does not recognise as part of the description, so writing a button line on a server that is
not using buttons would show `nbui1:btn:sunrise:F2A65A:Morning` to those users for no benefit. A
server that opts in accepts that trade knowingly; a server that never touches the setting must not
pay it.

Writing the button line is optional per playlist. A mix with no line still renders as a button when
the style is `button`; the client picks an icon from the mix kind (and, for time-of-day mixes, from
the slot, so morning and night do not get the same sun).

---

## Glyph vocabulary

**These are deliberately NOT SF Symbol names.** NaviBeat is two codebases, the Apple line and the
Kotlin line for Android, Linux and Windows, and they read the same wire. Writing `sunrise.fill`
would be pushing an Apple private vocabulary onto a server that also serves the other family.

```
sunrise  sun      sunset   moon
sparkles compass  heart    star
repeat   radio    shuffle  clock
gift     waveform
```

Each client maps these to its own icon set. Unknown values resolve to `waveform` rather than
failing, because the plugin will ship new names long before a client update reaches anyone.

---

## The 255 budget

`playlist.comment` is declared `varchar(255)` in Navidrome. It merely happens not to be enforced
today, which is not something to rely on.

Current three lines: about **165** characters. A button line adds about **40**. There is a test on
the client side (`test_theWholeThingStaysUnderNavidromesCommentLimit`) that fails if the intended
shape crosses 255. **If it gets tight, shorten the human sentence, never the machine line.**

Writing the hex without `#` is part of that budget, not a style choice.

---

## Rollout order, and it matters

1. **Client first, already done.** Shipping clients now strip *any* line matching `nb<alnum>:` from
   the description they show a person, so a future `nbui1:` line is never rendered as text.
2. **Plugin second, once that build is out.** If the plugin starts writing before then, users on
   older builds see `nbui1:btn:sunrise:F2A65A:Morning Mix` as part of the playlist description.
   Nothing breaks, but it looks like a bug.

The client half is additive and invisible until the plugin writes something, so there is no rush and
no coordination window to hit.

---

## What the plugin settings offer, as of 0.7.0

Two new groups in Navidrome's plugin settings:

- **How NaviBeat draws them**: `mixStyle`, one of `cover` (NaviBeat's generated artwork, the
  default), `button`, or `mosaic` (the server's own album grid).
- **Button icons and colours**: `icon<Family>` and `color<Family>` for the fifteen mix families
  (`iconMorning`, `colorGenreRadio` and so on). Every one is optional; empty means the built-in
  default, which is why a server nobody configures still gets a varied shelf.

The button LABEL is not a new field: it reuses the existing `name<Slot>` setting, so the button says
what the user already named that mix, without the playlist-name prefix a button is too narrow to
spend on.

Nothing was added to the NaviBeat app. Its old "NaviBeat covers for generated mixes" toggle was
removed on all five platforms in the same session, which is the entire point of this feature.

### These keys lost their dot in 0.9.7

They were `name.morning`, `icon.morning`, `color.morning` and so on until 0.9.7. Navidrome renders
the config schema with JsonForms, which reads a dot in a key as a path separator, so those fields
came up empty in the settings page and nothing typed into them ever reached the plugin (issue #5).
The keys are now `nameMorning`, `iconMorning`, `colorMorning`. The plugin still reads both older
shapes, the dotted key and the nested object JsonForms used to save, so an install configured before
the rename keeps its names, icons and colours.
