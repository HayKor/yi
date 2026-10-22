# yi

`yi` — CLI, который синхронизирует доску [Obsidian Kanban](https://github.com/mgmeyers/obsidian-kanban)
(`Kanban.md`) и заметки задач (task notes) с issues в трекере задач. Первый и единственный
на сегодня провайдер — GitHub; архитектура допускает добавление GitLab, SourceCraft и других
без изменения ядра (см. [«Adding a provider»](#adding-a-provider)).

Obsidian остаётся основным UI: вы редактируете карточки на доске и текст заметок прямо там,
а `yi` явными командами (`status` / `pull` / `push` / `new` / `start` / `done`) переносит
изменения между доской и remote-трекером. Никакой фоновой магии и скрытых id в `Kanban.md`.

## Установка / сборка

Пока модуль называется `kanban-sync` (не настоящий import path), поэтому `go install` работать
не будет — собирайте бинарник локально:

```sh
git clone <this-repo>
cd kanban-sync
make build          # -> ./yi (статический бинарник, CGO_ENABLED=0)
./yi version
```

После переноса модуля на настоящий репозиторий (например `github.com/you/yi`) можно будет
поставить его одной командой:

```sh
go install github.com/you/yi/cmd/yi@latest
```

Релизные бинарники для linux/darwin/windows (amd64/arm64) собираются через
[goreleaser](https://goreleaser.com) (`.goreleaser.yaml`); локальный прогон без публикации:

```sh
make release-snapshot   # goreleaser release --snapshot --clean
```

(goreleaser нужно установить отдельно, в репозитории он не требуется для `go build`/`go test`.)

## Быстрый старт

```sh
cd ~/Obsidian/01_Projects/MyApp
yi init          # создаёт .issue-sync.yml и Kanban.md (существующие файлы не трогает)
# впишите свой репозиторий в строку "repository:" в .issue-sync.yml
yi pull          # подтягивает все открытые issues на доску
```

`yi init` создаёт конфиг с четырьмя колонками (Backlog, Todo, In Progress, Done) и пустую
доску плагина Obsidian Kanban с теми же колонками. Папку `Tasks/` создаст `yi pull`.

### Структура проекта

```text
<project>/
├── .issue-sync.yml
├── Kanban.md
└── Tasks/
    ├── 42 Yandex OAuth.md
    └── 57 Analytics API.md
```

`<project>` — каталог, где лежит `.issue-sync.yml`; все относительные пути в конфиге
разрешаются от него. `yi` ищет конфиг в таком порядке: флаг `--project`, env `YI_PROJECT`,
поиск `.issue-sync.yml` вверх от текущего каталога, затем глобальный реестр проектов
(`$XDG_CONFIG_HOME/yi/config.yml`, иначе `~/.config/yi/config.yml`) — это позволяет запускать
`yi start 42` прямо из каталога с кодом, если он зарегистрирован там как `git.repository`.

### `.issue-sync.yml`

```yaml
provider: github
repository: my-org/yandex-ads

# Опционально, специфично для провайдера; ядро передаёт секцию провайдеру как есть.
github:
  baseUrl: https://github.example.com/api/v3/   # GitHub Enterprise

kanban: ./Kanban.md          # default: ./Kanban.md
tasks: ./Tasks               # default: ./Tasks

statuses:                    # порядок важен: это порядок колонок на доске
  backlog:
    heading: Backlog
    label: status:backlog
  todo:
    heading: Todo
    label: status:todo
  in-progress:
    heading: In Progress
    label: status:in-progress
  completed:
    heading: Completed
    label: status:completed
    closeIssue: true

defaultStatus: backlog       # статус для open issue без status-label; default: первый статус
startStatus: in-progress     # куда `yi start` переводит issue; default: "in-progress"
doneStatus: completed        # куда `yi done` переводит issue; default: первый статус с closeIssue

notes:
  create: always             # always | on-demand

git:                         # опционально; без секции "git" `yi start` не трогает git
  repository: ~/Projects/yandex-ads
  branches:
    template: "{type}/{issue}-{slug}"
    defaultType: feat
```

### Аутентификация в GitHub

Токен ищется в таком порядке: `GITHUB_TOKEN` → `GH_TOKEN` → `gh auth token` (если `gh` есть
в `PATH`). Токен никогда не сохраняется на диск. Самый простой вариант:

```sh
export GITHUB_TOKEN=ghp_xxx
# или
gh auth login
```

## Команды

Общие флаги: `--project <dir>` (каталог проекта), `-v` / `--verbose` (печатает в stderr путь
загруженного конфига и сколько issues пришло из remote: open, догруженные по одной, не найденные). Коды возврата: `0` успех, `1` ошибка выполнения, `2` ошибка
использования (неверные флаги/аргументы). `<id>` принимает и `42`, и `#42`.

### `yi init`

Создаёт в каталоге проекта (текущем или из `--project`) стартовые `.issue-sync.yml` и
`Kanban.md`. Уже существующий файл не перезаписывается (`skipped Kanban.md (already exists)`).
Не ходит в сеть и не трогает git.

### `yi status`

Показывает расхождения между доской и remote, ничего не меняет:

```text
$ yi status
Issue  Local     Remote    Title           State
#42    Todo      Todo      Yandex OAuth    ✓
#57    Backlog   Todo      Analytics API   !
-      Backlog   -         New card draft  +local
```

### `yi pull [--dry-run]`

Remote wins: переписывает доску (и создаёт заметки задач) так, чтобы она отражала remote.
Применяется сразу, без подтверждения — трогает только локальные файлы; `--dry-run` только
печатает план:

```text
$ yi pull --dry-run
Changes:
  #57 move: Backlog → Todo
  #57 create note for "Analytics API"
```

### `yi push [--dry-run] [--yes]`

Local wins: переносит изменения доски в remote (создание issues, смену статуса/labels,
переименование). Печатает diff и спрашивает подтверждение, если не указан `--yes`; в
неинтерактивном режиме (не TTY) без `--yes` команда откажется выполняться:

```text
$ yi push
Changes:
  create issue "New card draft" in Backlog with note
Apply 1 changes? [y/N] y
```

### `yi new "<title>" [--status <key>] [--body <text>]`

Создаёт issue, затем карточку на доске и заметку задачи (всегда, независимо от
`notes.create` — это явный запрос) одним шагом, без подтверждения — сама команда уже является
явным действием. Если создать issue не удалось, `Kanban.md` не меняется:

```text
$ yi new "Add OAuth refresh" --status todo
✓ Created #58 https://github.com/my-org/yandex-ads/issues/58
✓ Note Tasks/58 Add OAuth refresh.md
```

### `yi start <id> [--type <t>] [--no-branch]`

Переводит issue в `startStatus` локально и удалённо (создаёт карточку/заметку, если их ещё
нет), затем переключает (или создаёт) git-ветку по шаблону `git.branches.template`, если в
конфиге есть секция `git` и не передан `--no-branch`:

```text
$ yi start 42
✓ #42 moved to In Progress
✓ Remote issue updated
✓ Switched to feat/42-yandex-oauth
```

### `yi done <id>`

Переводит issue в `doneStatus`, ставит галочку на карточке и закрывает issue в remote:

```text
$ yi done 42
✓ #42 moved to Completed
✓ Remote issue closed
```

### `yi version`

Печатает версию (`-X main.version=...` при сборке, иначе `dev`).

## Как работает синхронизация

- Статус карточки на доске мапится в remote через labels `status:*` (одна на issue); статус в
  конфиге — это и заголовок колонки (`heading`), и имя label (`label`), в заданном порядке.
- `yi pull` — remote wins: доска переписывается под remote-состояние (статус, заголовок,
  закрытие), включая создание недостающих карточек/заметок.
- `yi push` — local wins: remote обновляется под состояние доски (новые issues, смена
  status-label, переименование), с diff и подтверждением перед применением.
- Режим заметок `notes.create`: `always` — заметка создаётся для каждой известной issue;
  `on-demand` — только когда явно запрошено (`yi new`, явная привязка карточки).
  Текст заметки (body) с issue не синхронизируется — это разные роли. Issue ID — единственный
  идентификатор, который связывает карточку, заметку и issue.
- `Kanban.md` не переписывается целиком: парсер построчный, всё незнакомое (frontmatter,
  архив плагина, `%% kanban:settings %%`, чужие колонки) остаётся opaque и не трогается;
  меняются только строки конкретных карточек, которые реально поменялись. Метаданные карточки
  (даты `@{…}`, время `@@{…}`, теги `#tag`) не входят в title и сохраняются при любой правке
  карточки; в title issue и в имя заметки они не попадают. `#12)` или `#45,` — ссылки на
  issue в тексте, не теги.
- Карточка может ссылаться на вашу собственную заметку (`[[Design doc|Login work]] `#7``):
  `yi` её не переименовывает и ссылку не переписывает — title такой карточки это alias
  (или имя заметки), и при переименовании issue меняется только alias.
- Запись `Kanban.md` всегда атомарна и с проверкой, что файл не изменился за время работы
  команды — иначе команда прерывается до записи, чтобы не потерять правки, сделанные в
  Obsidian параллельно.
- Сбой remote-операции посреди `push`/`start`/`done` не приводит к повторному созданию issues
  при повторном запуске: уже полученные id сохраняются до того, как операция прервётся.

## Adding a provider

Новый провайдер — это пакет `internal/provider/<name>`, который:

1. Реализует `provider.IssueProvider` (`ListOpenIssues`, `GetIssue`, `CreateIssue`,
   `UpdateIssue`) — см. `internal/provider/provider.go`.
2. Регистрирует себя в своём `init()`:

   ```go
   func init() {
       provider.Register("gitlab", New)
   }
   ```

3. Подключается к бинарнику единственной строкой blank-import в `cmd/yi/main.go`:

   ```go
   _ "kanban-sync/internal/provider/gitlab"
   ```

   Ядро (`internal/reconcile`, `internal/cli`) никогда не импортирует конкретный провайдер —
   только интерфейс `provider.IssueProvider` и реестр `provider.New`/`provider.Register`.

4. Обязан проходить общий conformance suite `providertest.Run(t, newProvider)`
   (`internal/provider/providertest`) — он гоняет create/get/list/update labels/close/reopen/
   `ErrNotFound` против любой реализации; пример — тесты `internal/provider/fake` и
   `internal/provider/github`. Фильтрация pull request'ов (`ListOpenIssues` их не возвращает,
   `GetIssue` отвечает `ErrNotFound`) — забота конкретного провайдера и его собственных тестов.

Заметки по конкретным трекерам (из дизайна, §12):

- **GitLab**: `iid` как `ID` issue; `add_labels`/`remove_labels` вместо раздельных вызовов;
  закрытие/открытие через `state_event: close|reopen`; токен `GITLAB_TOKEN`, иначе
  `glab auth token`; `gitlab.baseUrl` для self-hosted инсталляций.
- **SourceCraft**: сначала нужно исследовать API (числовой ли id, есть ли labels); если
  labels нет, провайдер может мапить статусы на свой собственный механизм — ядро этого не
  замечает, так как работает с `Status.Label` как с непрозрачной строкой.
