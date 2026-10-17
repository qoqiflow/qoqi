# Packages

A project gets the code of others the way PHP projects do with Composer: `qoqi.json` says what the project requires, `qoqi pkg update` works out versions that fit together and writes them in `qoqi.lock`, and `qoqi pkg install` puts exactly those in `vendor/`. The lock file is committed and `vendor/` is not, so every machine that runs `qoqi pkg install` gets the same code.

```bash
qoqi pkg require acme/textkit          # the newest release, required as ^1.4
qoqi pkg require acme/textkit:~1.2.0   # or with a constraint of your own
qoqi pkg require acme/testkit --dev    # only to develop and test the project
qoqi pkg install                       # what qoqi.lock says
qoqi pkg update                        # newer versions, as qoqi.json allows
qoqi pkg update acme/textkit           # only that package
qoqi pkg remove acme/textkit
qoqi show                          # what is installed
qoqi pkg show acme/textkit             # all about one package
qoqi pkg why acme/strings              # what requires it
qoqi pkg outdated                      # what has newer versions
```

```rust
import textkit;                     // then, as any module
import textkit.format.{pad};
```

## What Is Written Where

| File | Written by | Says | Committed |
|------|------------|------|-----------|
| `qoqi.json` | you, and `require` / `remove` | The packages the project requires, and where to find those not at the default place | yes |
| `qoqi.lock` | `update`, `require`, `remove` | The exact version and commit of every package, those required by packages too | yes |
| `vendor/` | `install` (and the three above) | The files of the packages | no: add `vendor/` to `.gitignore` |
| `vendor/installed.json` | `install` | Where the modules of each package are, for the compiler | no |

## `qoqi.json`

```json
{
    "name": "acme/shop",
    "entry": "src/main.qi",
    "root": "src",
    "require": {
        "acme/textkit": "^1.4",
        "acme/json-schema": "~2.1.0"
    },
    "require-dev": {
        "acme/testkit": "*"
    },
    "repositories": [
        { "type": "git", "url": "https://git.example.com/team/json-schema.git" },
        { "type": "path", "url": "../testkit" }
    ]
}
```

| Key | Meaning |
|-----|---------|
| `require` | The packages the program needs, by name and [version constraint](#version-constraints) |
| `require-dev` | Those only its development needs: tests, tools. `qoqi pkg install --no-dev` leaves them out |
| `repositories` | Where packages come from besides the default place ([below](#where-packages-come-from)) |
| `name` | The name of the package, `vendor/name`, when others require it. Lower case letters, digits, and `_ . -` between them |
| `import` | The name its modules are imported by. Default: the last part of `name`, with `-` and `.` as `_`: `acme/text-kit` is `import text_kit;` |
| `version` | The version of a package kept in a folder (a `path` repository). Packages in git take theirs from tags |
| `description`, `license` | Shown by `qoqi pkg show`; informative |

The other keys (`entry`, `root`, `packages`, `heap_mb`, `max_depth`) are those of [project.md](../configuration.md). `require` and `remove` write `qoqi.json` back with every key kept in its place, indented by four spaces.

## Version Constraints

As Composer writes them:

| Constraint | Accepts |
|------------|---------|
| `1.2.3`, `=1.2.3` | That version |
| `^1.2.3` | `>=1.2.3 <2.0.0`: nothing that breaks, by the rules of [semantic versioning](https://semver.org). The usual choice |
| `^0.3.1` | `>=0.3.1 <0.4.0`: before 1.0 a minor version may break |
| `~1.2` | `>=1.2.0 <2.0.0`: the last number written may grow |
| `~1.2.3` | `>=1.2.3 <1.3.0` |
| `1.2.*`, `1.2` | Every `1.2.x` |
| `*` | Every version |
| `>=1.2 <2.0`, `>=1.2, <2.0` | Several comparisons, all of which hold: `>`, `>=`, `<`, `<=`, `!=` |
| `1.0 - 2.1` | `>=1.0.0 <2.2.0`: both ends included |
| `^1.0 \|\| ^2.0` | Either |
| `dev-main` | The branch `main`, at its last commit when the lock was written |

A pre-release, `2.0.0-beta.1`, is only taken by a constraint that names a pre-release of the same version: `^2.0.0-beta` accepts it, `^1.0` and `>=1.0` never do.

`qoqi pkg require vendor/name` without a constraint requires `^` of the newest release: `^1.4` for 1.4.2, `^0.3.1` for 0.3.1. A package without releases is required at its default branch (`dev-main`); a folder package without a version at `*`.

## Resolving

`qoqi pkg update` chooses one version of every package for the whole program, as Composer does: the project's requirements, those of the packages it requires, and so on, all met at once. Two packages that require different major versions of a third cannot both be installed; the message says who requires what:

```
No version of acme/log meets every requirement on it:
  - qoqi.json requires acme/log ^1.0
  - acme/http 2.0.0 requires acme/log ^2.0
  the versions there are: 2.1.0, 2.0.0, 1.4.2, 1.0.0, dev-main
```

The newest version that fits is chosen; where it leads nowhere, the search goes back and tries an older one. `qoqi pkg update acme/textkit` moves that package, and what it needs, and keeps the others at their locked versions when they still fit. `require` and `remove` do the same for the packages they name. When resolving fails, `require` puts `qoqi.json` back as it was.

## Installing

`qoqi pkg install` reads `qoqi.lock` and makes `vendor/` match it: it installs what is missing, changes what is at another version, and removes what the lock no longer has. Without a lock file it resolves first, as `update` would. When `qoqi.json` was changed since the lock was written, it says so and installs the lock anyway; `qoqi pkg update` brings the lock up to date.

A program whose `qoqi.json` requires a package that is not installed does not compile:

```
Error: qoqi.json requires acme/textkit, which is not installed: run qoqi pkg install
```

## Looking at the Packages

`qoqi pkg outdated` asks the repositories what is newer than what is locked:

```
acme/dates    dev-main  ! dev-main 3f2a9c1  qoqi pkg update acme/dates
acme/greet    1.2.0     ! 1.3.0             qoqi pkg update acme/greet
acme/strings  2.1.0     ~ 3.0.0             beyond what is required

! an update the requirements allow: qoqi pkg update brings it
~ a new major version, beyond the requirements: change them to have it
```

A pre-release counts only for a package installed at one; a package kept in a folder is never outdated.

`qoqi pkg why acme/strings` says what requires a package and with what constraint, and `qoqi pkg show acme/greet` all there is to know about one: its version and commit, where it comes from, the name it is imported by, the folder of its modules, what it requires and what requires it.

## Where Packages Come From

**Git.** A package is a git repository. Its versions are its tags (`v1.4.2` or `1.4.2`; tags that are not versions are ignored), its branches are `dev-<branch>`. The repository is looked for in the `repositories` of `qoqi.json`: for each `git` one, its `qoqi.json` on the default branch says what package it is. A name none of them has is fetched from GitHub, `https://github.com/<vendor>/<name>.git`.

Any address git understands works: `https://`, `ssh://`, `git@host:team/repo.git`, a folder. Fetching is git's job, with its credentials: a private repository needs what `git clone` of it would need. git is never left asking for a password, which nobody would see: use an SSH address (`git@host:team/repo.git`) or a credential helper for a private repository over HTTPS. git and tar have to be installed.

**A folder.** `{ "type": "path", "url": "../testkit" }` uses a package where it is, without copying it: changes to it are seen at once, which is what working on a package and a project together needs. Its version is the `version` of its `qoqi.json`, or, without one, it accepts every constraint (`dev-path`).

**The cache.** Repositories are kept as mirrors in a cache, so that a package is downloaded once per machine and `qoqi pkg install` from a lock needs no network when the cache has the commits. It is in `~/.cache/qoqi` (`$XDG_CACHE_HOME/qoqi` if that is set, `%LOCALAPPDATA%\qoqi\cache` on Windows); `QOQI_CACHE` puts it elsewhere. Deleting it is always safe.

**Elsewhere by default.** `QOQI_PACKAGE_URL` replaces the GitHub address, with `{name}` for the package's name: `https://git.example.com/{name}.git` makes a company's server the default place.

## Making a Package

A package is a project like any other, with a `name`. Its modules are those under its import name, starting at its `root`:

```
textkit/                      git repository acme/textkit, tagged v1.4.2
  qoqi.json                  { "name": "acme/textkit", "root": "src", "require": { "acme/strings": "^2.0" } }
  src/
    textkit.qi               import textkit;
    textkit/
      format.qi              import textkit.format;
      format_test.qi         qoqi test, from the package's folder
```

Inside the package, modules import each other by the same names (`import textkit.format;`), and the packages it requires by theirs, so its own tests run as they will in a project that requires it. A project that requires it reads `vendor/acme/textkit/src/textkit.qi` for `import textkit;`.

To release a version, tag a commit: `git tag v1.5.0 && git push --tags`. Versions follow [semantic versioning](https://semver.org): a change that can break a program that uses the package is a new major version, so that `^1.4` never takes it.

Two packages imported by the same name cannot be in one program; one of them has to say another `import` in its `qoqi.json`. A package's name in `qoqi.json` has to be the one it is required by.

## Compared to Composer

The same: `require`, `require-dev`, `repositories`, `qoqi.lock` with its `content-hash`, `vendor/`, the constraint syntax, one version of each package for the program, the commands (`install`, `update`, `require`, `remove`, `show`, `why`, `outdated`) and what they print.

Not there yet:

*   **A registry** like Packagist, which knows every package and its versions without cloning it. GitHub, or `QOQI_PACKAGE_URL`, stands in for it, and a repository is cloned to learn its versions.
*   **Archives.** Packages are fetched with git, never as a zip of a release.
*   **Stability flags** (`@beta`, `minimum-stability`), `provide`, `replace`, `conflict`, scripts and plugins.
*   **Checks a SAT solver would do in one go.** The resolver searches, skipping the versions that would fail for a reason already found. A large graph full of conflicts can take it longer than Composer.

## How It Is Built

The package manager is a Qoqi program, in [`internal/pkgmgr/lib/`](../../../internal/pkgmgr/lib), compiled into the `qoqi` binary like `qoqi fmt` and the language server:

| Module | Does |
|--------|------|
| `main.qi` | The commands, and what they print |
| `manifest.qi` | Reads `qoqi.json`, writes back a requirement |
| `catalog.qi` | What the resolver asks of a source of packages: a trait, `Catalog` |
| `resolver.qi` | Chooses the versions, against any `Catalog`; the tests give it one kept in memory |
| `sources.qi` | The real `Catalog`: git mirrors and folders |
| `lockfile.qi` | Reads and writes `qoqi.lock` |
| `installer.qi` | Fills `vendor/` and writes `vendor/installed.json` |
| `inspect.qi` | `show`, `why` and `outdated` |

Versions and constraints are [`std.semver`](stdlib.md#stdsemver), which any program can use. The compiler only reads `vendor/installed.json` (in `internal/project`), which maps each import name to a folder of modules the way `"packages"` does.
