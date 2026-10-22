# `yi` — синхронизация Obsidian Kanban с issue tracker'ами

Дата: 2026-10-21
Статус: design, ожидает review
Исходная идея: [obsidian-kanban-git-integration.md](../../obsidian-kanban-git-integration.md)

## 1. Цель и рамки

`yi` — standalone CLI, который связывает `Kanban.md` (плагин Obsidian Kanban) и task notes
в Obsidian с issues удалённого tracker'а. Первый провайдер — GitHub. Архитектура должна
позволять добавить GitLab, SourceCraft и др. без изменений ядра.

### Входит в первую итерацию

| Команда | Что делает |
|---|---|
| `yi status` | Показывает расхождения local ↔ remote. Ничего не меняет. |
| `yi pull` | Remote → Obsidian (remote wins). |
| `yi push` | Obsidian → Remote (local wins), с diff и подтверждением. |
| `yi new "Title"` | Создаёт issue + note + карточку. |
| `yi start <id>` | Переводит в In Progress локально и удалённо, создаёт/переключает git branch. |
| `yi done <id>` | Переводит в Completed, ставит `[x]`, закрывает issue. |

### Не входит (явно)

`yi pr`, `yi agent`, `yi sync` (bidirectional + state file), GitLab/SourceCraft провайдеры,
`yi init`, хранение токенов в keyring, GitHub Projects v2. Архитектура учитывает их появление
(см. §12), но код под них не пишется.

### Критерии успеха

1. `parse → write` без изменений даёт байт-в-байт тот же `Kanban.md` (включая frontmatter
   `kanban-plugin`, блок `%% kanban:settings %%`, архив, неизвестные колонки, многострочные карточки).
2. Повторный `yi pull` / `yi push` сразу после предыдущего — no-op (идемпотентность).
3. Сбой посреди `push` не приводит к дублированию issues при повторном запуске.
4. Новый провайдер = новый пакет `internal/provider/<name>` + одна строка импорта; проходит
   общий conformance test suite.
5. Один статический бинарник для linux/macOS/windows (amd64/arm64).

## 2. Технологии

- **Go** (≥1.24, `CGO_ENABLED=0`) — статический бинарник, тривиальная кросс-компиляция.
- `github.com/spf13/cobra` — CLI.
- `gopkg.in/yaml.v3` — конфиг (через `yaml.Node`, чтобы сохранить порядок статусов).
- `github.com/google/go-github/vNN` — GitHub REST API (последняя мажорная версия на момент старта).
- `git` вызывается через `os/exec` (уважает пользовательские конфиги, хуки, credential helpers).
- Сборка: `go build`, релизы — goreleaser (после MVP), версия через `-ldflags`.

## 3. Структура Obsidian-проекта

```text
<project>/
├── .issue-sync.yml
├── Kanban.md
└── Tasks/
    ├── 42 Yandex OAuth.md
    └── 57 Analytics API.md
```

`<project>` — каталог с `.issue-sync.yml`. Все относительные пути конфига разрешаются от него.

## 4. Конфигурация

```yaml
provider: github
repository: my-org/yandex-ads

# Опционально, специфично для провайдера. Ядро передаёт секцию провайдеру как есть.
github:
  baseUrl: https://github.example.com/api/v3/   # GitHub Enterprise

kanban: ./Kanban.md          # default: ./Kanban.md
tasks: ./Tasks               # default: ./Tasks

statuses:                    # порядок важен: это порядок колонок
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

defaultStatus: backlog       # open issue без status-label; default: первый статус
startStatus: in-progress     # куда двигает `yi start`; default: "in-progress"
doneStatus: completed        # куда двигает `yi done`; default: первый статус с closeIssue

notes:
  create: always             # always | on-demand

git:                         # опционально; без секции `yi start` не трогает git
  repository: ~/Projects/yandex-ads
  branches:
    template: "{type}/{issue}-{slug}"
    defaultType: feat
```

### Валидация (ошибка при загрузке)

- `provider` зарегистрирован; `repository` не пуст.
- `statuses` не пуст; `heading` уникальны после `domain.NormalizeHeading` (так же, как
  колонки доски матчатся на статусы, §6.2), `label` уникальны без учёта регистра.
- Секция `git` без `repository` — ошибка `git.repository is required`.
- Ровно один статус с `closeIssue: true` (закрытый issue однозначно мапится в колонку).
- `defaultStatus`, `startStatus`, `doneStatus` ссылаются на существующие ключи;
  `doneStatus` имеет `closeIssue: true`.
- `notes.create` ∈ {`always`, `on-demand`}.
- `~` в путях раскрывается.

### Поиск проекта

По порядку, первое найденное:

1. `--project <dir>` (глобальный флаг).
2. `YI_PROJECT` env.
3. Поиск `.issue-sync.yml` вверх от cwd.
4. Глобальный реестр `~/.config/yi/config.yml` (`$XDG_CONFIG_HOME`):
   ```yaml
   projects:
     - ~/Obsidian/01_Projects/yandexAds
   ```
   Выбирается проект, у которого `git.repository` содержит cwd. Это позволяет запускать
   `yi start 42` прямо из каталога с кодом.

Иначе — ошибка с подсказкой.

## 5. Доменная модель (`internal/domain`)

```go
type IssueState int
const (
    IssueOpen IssueState = iota
    IssueClosed
)

type Issue struct {
    ID        int
    Title     string
    State     IssueState
    Labels    []string
    URL       string
    UpdatedAt time.Time
}

type StatusKey string

type Status struct {
    Key        StatusKey
    Heading    string
    Label      string
    CloseIssue bool
}
```

Issue ID — `int` (GitHub number, GitLab iid). Это главный идентификатор задачи; своих ID нет.

## 6. Архитектура

```text
cmd/yi/main.go                точка входа; blank-import провайдеров
internal/
  domain/                     Issue, Status — без зависимостей
  config/                     Load, Validate, Resolve(project); Config.StatusOf(issue)
  kanban/                     Board: Parse/Bytes, Cards(), Move, Add, SetChecked, SetIssueID, SetLink, SetTitle
  notes/                      Store: Find(id), Create(id, title), Rename(id, newTitle); filename sanitize
  provider/                   IssueProvider, Registry, ошибки
    github/                   реализация через go-github + auth
    fake/                     in-memory провайдер для тестов
    providertest/             conformance suite для любого провайдера
  gitrepo/                    Repository: CurrentBranch, BranchExists, Switch, CreateAndSwitch
  branch/                     Name(template, type, id, title) + slug/транслитерация
  reconcile/                  Planner (чистые функции) + Applier (side effects)
  cli/                        cobra-команды; тонкий слой: собрать зависимости → sync → вывод
```

Правила зависимостей: `domain` ни от чего не зависит; `kanban`, `notes`, `branch`, `gitrepo`
знают только `domain`; `provider/*` знает только `domain`; `reconcile` знает `domain`, `config`,
`kanban`, `notes`, `provider` (интерфейс); только `cli` и `cmd` собирают всё вместе.
Ядро никогда не импортирует `provider/github`.

### 6.1 Провайдеры (`internal/provider`)

```go
type IssueProvider interface {
    // Только open issues. Pull requests отфильтрованы. Пагинация внутри.
    ListOpenIssues(ctx context.Context) ([]domain.Issue, error)
    // Любое состояние. ErrNotFound, если issue нет/удалён/перенесён.
    GetIssue(ctx context.Context, id int) (domain.Issue, error)
    CreateIssue(ctx context.Context, in NewIssue) (domain.Issue, error)
    // Применяет только заданные поля; возвращает актуальное состояние.
    UpdateIssue(ctx context.Context, id int, upd IssueUpdate) (domain.Issue, error)
}

type NewIssue struct {
    Title  string
    Body   string
    Labels []string
}

type IssueUpdate struct {
    Title        *string
    AddLabels    []string
    RemoveLabels []string
    State        *domain.IssueState
}

type Config struct {
    Name       string     // "github"
    Repository string     // "owner/repo"
    Options    yaml.Node  // сырая секция `github:` / `gitlab:`; провайдер декодирует сам
}

type Factory func(ctx context.Context, cfg Config) (IssueProvider, error)

func Register(name string, f Factory)   // вызывается из init() пакета провайдера
func New(ctx context.Context, cfg Config) (IssueProvider, error)
func Names() []string

var (
    ErrNotFound = errors.New("issue not found")
    ErrAuth     = errors.New("authentication failed")
)
```

Почему `AddLabels`/`RemoveLabels`, а не замена набора: не затираем чужие labels и не
получаем гонку с параллельными правками; в GitLab это нативные `add_labels`/`remove_labels`,
в GitHub — `AddLabelsToIssue` + `RemoveLabelForIssue`. Несуществующие labels GitHub и GitLab
создают автоматически, поэтому отдельного `EnsureLabels` нет.

Почему `ListOpenIssues` + `GetIssue`, а не «все issues»: у старых репозиториев тысячи закрытых
issues. Закрытые нужны только для карточек, которые уже на доске, — их догружаем точечно
(параллельно, ограничение 8 запросов).

#### GitHub (`internal/provider/github`)

- Токен: `GITHUB_TOKEN` → `GH_TOKEN` → `gh auth token` (если `gh` в `PATH`; при заданном
  `baseUrl` — `gh auth token --hostname <host baseUrl>`) → `ErrAuth` с подсказкой. Токены
  нигде не сохраняются.
- `github.baseUrl` → `github.NewClient(...).WithEnterpriseURLs`.
- `ListOpenIssues`: `Issues.ListByRepo(state=open, per_page=100)` с пагинацией; элементы с
  `PullRequestLinks != nil` отбрасываются. `GetIssue` для pull request возвращает
  `ErrNotFound` — `yi` никогда не меняет labels/state у PR.
- `UpdateIssue`: title/state — `Issues.Edit`; labels — `AddLabelsToIssue`, затем
  `RemoveLabelForIssue` для каждой (404 на remove игнорируется).
- HTTP 401/403 → `ErrAuth`, 404/410 → `ErrNotFound`, обёрнутые с контекстом.

### 6.2 Kanban (`internal/kanban`)

Не полноценный Markdown-parser: строгий построчный формат, всё неизвестное — opaque.

**Модель.** `Board` хранит исходные строки (с исходными окончаниями `\n`/`\r\n`) и индекс:

- **Section** — строка `## <heading>` и всё до следующего `## ` или до строки `***`
  (разделитель архива плагина), или до `%% kanban:settings`. Секция «configured», если
  heading совпадает с `heading` статуса после `domain.NormalizeHeading` (trim,
  case-insensitive, без суффикса WIP-лимита `\s*\(\d+\)`: `## In Progress (3)`). Тот же
  нормализатор использует валидация конфига для проверки уникальности `heading`. Неизвестные секции,
  preamble (frontmatter и всё до первой секции), архив и settings — opaque, не изменяются.
- **Card** — в configured-секции строка `^- \[( |x|X)\] ` в колонке 0 плюс следующие строки с
  отступом (пробел/таб) — continuation (многострочные карточки плагина). Пустая строка
  завершает карточку.

**Разбор карточки** (по первой строке):

- `IssueID` — последнее вхождение `` `#(\d+)` ``. Голый `#42` не считается (в Obsidian это тег).
- `Link` — первый `[[target]]` или `[[target|alias]]`.
- `Link` не может быть датой: `@[[…]]` — это дата (daily note), а не ссылка карточки.
- `Meta` — метаданные плагина в порядке появления: даты `@{…}` / `@[[…]]`, время `@@{…}`,
  теги — `#` в начале строки или после пробела, за которым серия `[\p{L}\p{N}_/-]`
  (пунктуация завершает тег: `#urgent.` → тег `#urgent`), не состоящая только из цифр, вне
  backticks и вне `[[link]]`. Не теги: `#42`, `#12)`, `#45,`, `#123.` (ссылки на issue в
  тексте), `C#`, `[[Note#Heading]]`; теги: `#urgent`, `#area/ui`, `#v2`.
- `Title`:
  - есть link → alias, если есть; иначе basename target без префикса `<id> `;
  - нет link → текст без checkbox, без `` `#id` `` и без `Meta`, пробелы схлопнуты.
  Title никогда не содержит `Meta`: в title issue (CreateIssue/RenameRemote) и в имя note
  метаданные не попадают; remote title при сравнении (§6.4) и при записи в карточку/имя note
  тоже берётся без них.
- `Checked`, `Status` (по секции), позиция (индексы строк).

**Каноничная карточка** (то, что пишет `yi`):

```md
- [ ] [[Tasks/42 Yandex OAuth]] `#42`     ← есть note
- [ ] Yandex OAuth `#42`                  ← note нет (notes.create: on-demand)
```

Путь ссылки — путь note относительно `<project>` без `.md`.

**Операции** (все возвращают ошибку, Board — изменяемый объект):

- `Move(id, status)` — вырезать строки карточки, вставить в целевую секцию. Точка вставки:
  после последней карточки секции; если карточек нет — сразу после heading (через одну
  пустую строку, как делает плагин). Continuation-строки едут вместе с карточкой.
  В секции без карточек, где перед пустыми строками есть текст (маркер `**Complete**`, который
  плагин пишет в «complete»-колонке), карточка встаёт сразу под последней непустой строкой
  этого текста — как пишет плагин.
- `Add(status, line)` — вставка по тем же правилам.
- `SetChecked`, `SetIssueID` (дописывает `` `#id` `` в конец строки), `SetLink` (заменяет
  только токен `[[...]]`, сохраняя alias) — точечные правки: даты `@{…}`, теги и прочий текст
  карточки сохраняются.
- `SetTitle(card, text)` — пересобирает первую строку как `- [<box>] <text>` + ` ` + `Meta`
  (в исходном порядке, через пробел) + `` `#id` `` (если есть); continuation-строки не
  трогаются. `text` — title или `[[link]]`. Используется там, где меняется сама структура
  link-less карточки: plain-карточка получает note (LinkNote, CreateIssue с note), или
  переименовывается plain-карточка (RenameLocal). LinkNote планируется только для карточки
  без link: карточка со ссылкой, которую `yi` не разрешил (shortest-path link Obsidian), не
  перепривязывается.
- Если для статуса нет секции — секция `## <heading>` создаётся после ближайшей предыдущей
  configured-секции (или перед первой opaque-секцией архива/settings), с пустой строкой
  вокруг.
- `Bytes()` — склейка строк. Без изменений = исходный файл байт-в-байт.

**Checkbox.** Статус определяется колонкой, не checkbox'ом. `yi` трогает checkbox только при
перемещении: в статус с `closeIssue` → `[x]`, из него → `[ ]`. Немодифицированные карточки
(например, неотмеченные в «Done», как на существующих досках) не трогаются.

**Дубликаты.** Один `#id` в двух карточках — ошибка до любых изменений, с номерами строк.

### 6.3 Task notes (`internal/notes`)

- **Поиск note для issue**: (1) target ссылки карточки → `<project>/<target>.md`, если
  существует; (2) файл в `tasks/` с frontmatter `issue: <id>`; (3) файл `tasks/<id> *.md`.
- **Имя файла**: `<id> <sanitize(title)>.md`. `sanitize` удаляет `\ / : * ? " < > | # ^ [ ]`,
  схлопывает пробелы, обрезает до 100 символов.
- **Шаблон** (встроенный):
  ```md
  ---
  issue: 73
  ---

  # Creative Preview

  ## Context

  ## Implementation plan

  ## Notes
  ```
- **Rename(id, newTitle)**: переименовать файл; если H1 note совпадает со старым title —
  заменить на новый. Остальное содержимое не трогается. Ссылки на note из других файлов vault
  не обновляются — `yi` печатает предупреждение.
- **Создание по режиму**: `always` — note создаётся в pull/push/new/start; `on-demand` — только
  в `new` и `start` (pull/push создают карточку без note). Существующая note никогда не
  перезаписывается.

### 6.4 Сравнение заголовков

Заголовок в файловой системе — lossy (`Fix: crash` → `Fix crash`). Поэтому title считается
расходящимся, только если `sanitize(remote) != sanitize(local)` (обе стороны — без
метаданных карточки, §6.2). Иначе после каждого pull
был бы вечный drift, а push затирал бы `:` в remote.

### 6.5 Remote → local статус

`config.StatusOf(issue)`:

1. `closed` → статус с `closeIssue: true`.
2. `open` + status-labels → первый в порядке конфига статус, чей label есть у issue (labels
   сравниваются без учёта регистра, как в GitHub; `SetStatus` удаляет label в написании issue). Если
   status-labels несколько — warning в `status`/`pull`.
3. `open` без status-label → `defaultStatus`.

`open` + label статуса с `closeIssue` (например, `status:completed`) даёт этот статус;
`status` покажет его как «open, но в Completed», а `push` закроет issue.

### 6.6 Синхронизация (`internal/reconcile`)

Пакет назван `reconcile`, а не `sync`, чтобы не конфликтовать со стандартным `sync`.

Planner — чистые функции: на входе `Board`, `map[int]Issue` (open + догруженные), `Config`,
`notes.Index`; на выходе `Plan{Changes []Change, Warnings []Warning}`. Никакого IO.

```go
type Change interface{ isChange() }

// local
type MoveCard     struct{ ID int; From, To StatusKey }
type AddCard      struct{ Issue domain.Issue; To StatusKey; WithNote bool }
type RenameLocal  struct{ ID int; From, To string }      // note + карточка
type LinkNote     struct{ ID int; Title string }         // создать note, переписать карточку

// remote
type CreateIssue  struct{ Card *kanban.Card; Title, Body string; Status StatusKey; WithNote bool }
type SetStatus    struct{ ID int; From, To StatusKey; Update provider.IssueUpdate } // labels + close/reopen
type RenameRemote struct{ ID int; From, To string }
```

#### `status`

Таблица: Issue | Local | Remote | Title | State. Строки:
`✓` совпадает; `!` статус/title различаются; `+local` карточка без id (создастся при push);
`+remote` open issue не на доске (добавится при pull); `?` id на доске, но `ErrNotFound`.
Внизу — warnings (дубликаты status-labels и т. п.). Цвет только при TTY и без `NO_COLOR`.

#### `pull` (remote wins)

Для карточек с id:

- issue `ErrNotFound` → warning, карточка не трогается;
- статус отличается → `MoveCard`;
- title отличается (§6.4) → `RenameLocal`;
- `notes.create: always` и note нет → `LinkNote`.

Open issue не на доске → `AddCard` в его статус (`WithNote` = режим `always`).
Карточки без id не трогаются (это зона push). Remote не изменяется.

План печатается; изменения применяются без подтверждения (только локальные файлы).
`--dry-run` — только печать.

#### `push` (local wins)

Для карточек с id (и существующих в remote):

- status-labels не равны {label колонки} → `SetStatus` (add недостающий, remove прочие
  status-labels; не-статусные labels не трогаются);
- колонка с `closeIssue` и issue open → close; колонка без `closeIssue` и issue closed →
  reopen (в том же `SetStatus`);
- title отличается (§6.4) → `RenameRemote`.

Карточки без id → `CreateIssue` (labels = label колонки; если колонка с `closeIssue` —
создать и сразу закрыть). Body пустой.
Open issues не на доске → не трогаются (видны в `status` как `+remote`).

Печатается diff, затем запрос `Apply N changes? [y/N]`. `--yes` пропускает вопрос;
без TTY и без `--yes` — отказ с ошибкой. `--dry-run` — только печать.

#### Applier и устойчивость к сбоям

1. Перед записью: `Kanban.md` перечитывается и сравнивается с прочитанным в начале
   (sha256). Если пользователь изменил файл в Obsidian во время работы — abort, ничего не
   пишется локально, сообщение «re-run».
2. `push`: remote-операции выполняются последовательно. После каждого успешного
   `CreateIssue` его id сразу вносится в in-memory Board. **При ошибке на середине** Board
   всё равно сохраняется (с уже полученными id), затем возвращается ошибка — повторный
   `push` не создаст дубликаты.
   Если при этом сработал пункт 1 (файл изменён), `Kanban.md` не пишется, а `yi` печатает
   для каждого созданного issue строку «add `#73` to card "Title"», чтобы пользователь
   вписал id вручную.
3. Порядок локальной записи: сначала notes (create/rename), потом `Kanban.md`.
4. `Kanban.md` пишется атомарно: temp-файл в том же каталоге → `fsync` → `rename`,
   права исходного файла сохраняются.

### 6.7 Команды `new` / `start` / `done`

**`yi new "<title>" [--status <key>] [--body <text>]`** — `provider.CreateIssue` (label
статуса, default `defaultStatus`; для статуса с `closeIssue` — сразу close) → `AddCard`
(`WithNote` всегда, это явный запрос) → запись. Неудачный create ничего не пишет в
`Kanban.md` (нет карточки без id, из которой push сделал бы дубликат); неудачный close —
карточка всё равно добавляется, затем возвращается ошибка. Выводит `#id`, URL и путь note.

**`yi start <id> [--type <t>] [--no-branch]`**

1. `GetIssue(id)` (нет → ошибка).
2. Локально: карточка → `startStatus` (если карточки нет — `AddCard`); note создаётся,
   если её нет (в обоих режимах).
3. Remote: `SetStatus` → label `startStatus`; если closed — reopen.
4. Git (если есть `git.repository` и не `--no-branch`): имя ветки из шаблона;
   ветка существует → `git switch <name>`, иначе `git switch -c <name>` от текущего HEAD.
   Ошибки git (грязное дерево и т. п.) показываются как есть; локальные/remote изменения к
   этому моменту уже применены, это сообщается явно.

```text
✓ #42 moved to In Progress
✓ Remote issue updated
✓ Switched to feat/42-yandex-oauth
```

**`yi done <id>`** — карточка → `doneStatus` + `[x]` (и для неотмеченной карточки, уже
стоящей в `doneStatus`); remote: label `doneStatus` + close.

### 6.8 Имена веток (`internal/branch`)

- Плейсхолдеры: `{type}`, `{issue}`, `{slug}`. Неизвестный плейсхолдер — ошибка конфига.
- `slug`: транслитерация кириллицы (упрощённая ГОСТ 7.79-2000 Б: `щ→shch`, `ж→zh`, `х→kh`,
  `ц→c`, `ъ/ь→""`…), lowercase, всё кроме `[a-z0-9]` → `-`, схлопывание `-`, trim `-`,
  не длиннее 50 символов (обрезка по последнему `-`).
- Пустой slug → шаблон без хвоста: `feat/42` (trim висящих `-`/`/`).
- Результат проверяется `git check-ref-format --branch`.

`"Добавить preview generated creative"` → `feat/73-dobavit-preview-generated-creative`.

### 6.9 Git (`internal/gitrepo`)

```go
type Repository interface {
    CurrentBranch(ctx) (string, error)
    BranchExists(ctx, name string) (bool, error)   // refs/heads/<name>
    Switch(ctx, name string) error
    CreateAndSwitch(ctx, name string) error
}
```

Реализация: `exec.CommandContext("git", "-C", dir, ...)`, stderr git включается в ошибку.
Отдельна от `IssueProvider` (в будущем `PRProvider` тоже будет отдельным, §12).

## 7. CLI

```text
yi [--project <dir>] [-v] <command>

  status
  pull   [--dry-run]
  push   [--dry-run] [--yes]
  new    <title> [--status <key>] [--body <text>]
  start  <id> [--type <t>] [--no-branch]
  done   <id>
  version
```

- `<id>` принимает `42` и `#42`.
- Exit codes: `0` успех; `1` ошибка; `2` ошибка использования (cobra).
- `-v` — печатает в stderr путь загруженного конфига и счётчики remote issues (open,
  догруженные по одной, не найденные).
- `ctx` с `signal.NotifyContext` (Ctrl+C отменяет сетевые запросы; локальная запись атомарна).

## 8. Обработка ошибок

| Ситуация | Поведение |
|---|---|
| Нет конфига | Ошибка с перечнем мест поиска (§4) |
| Невалидный конфиг | Все ошибки валидации списком, с путём поля |
| Нет `Kanban.md` | Ошибка (создание доски — будущий `yi init`) |
| Дубликат `#id` на доске | Ошибка с номерами строк, до любых изменений |
| Нет токена | `ErrAuth` + подсказка: `export GITHUB_TOKEN=…` или `gh auth login` |
| Issue с доски не найден в remote | Warning, карточка пропускается |
| Несколько status-labels | Warning; берётся первый по порядку конфига |
| `Kanban.md` изменён во время работы | Abort до локальной записи |
| Сбой remote-операции в push | Сохраняем уже полученные id, затем ошибка (§6.6) |
| Rate limit GitHub | Ошибка с временем сброса из ответа (без автоматических retry в MVP) |

## 9. Тестирование

- **kanban** — golden-файлы в `testdata/`: пример из идеи; реальная доска плагина
  (frontmatter `kanban-plugin: board`, `%% kanban:settings %%`, `***` + `## Archive`,
  многострочные карточки, неизвестные колонки, `\r\n`, heading в другом регистре —
  «In progress»). Тест round-trip: `Bytes(Parse(x)) == x` для всех fixtures. Тесты
  `Move`/`Add`/создания секции сравнивают с ожидаемым golden-выводом.
- **config** — валидация, default'ы, порядок статусов, поиск проекта (temp dirs, env).
- **notes** — sanitize, поиск по 3 стратегиям, создание, rename с H1.
- **branch** — таблица slug'ов (кириллица, пунктуация, пустой, длинный).
- **sync** — table-driven тесты Planner на чистых данных; Applier — с `provider/fake` и temp
  dir: идемпотентность (второй прогон = пустой план), сбой на N-й операции (fake с
  инъекцией ошибки) → id сохранены, повтор не плодит issues.
- **provider/providertest** — conformance suite: `Run(t, newProvider func(t) IssueProvider)`.
  Проверяет create/get/list/update labels/close/reopen/ErrNotFound (фильтрация PR —
  в тестах конкретного провайдера). Гоняется
  против `fake` и против `github` поверх `httptest.Server`, эмулирующего нужные эндпоинты.
  Каждый будущий провайдер обязан проходить этот suite.
- **gitrepo** — на временном репозитории (`git init` в `t.TempDir()`), пропуск, если `git`
  нет в PATH.
- **cli** — e2e: root-команда с `fake` провайдером, temp-проект, проверка stdout и файлов.
- Опционально, вне CI: `YI_E2E_GITHUB_REPO` + токен — smoke-тест против настоящего
  тестового репозитория.

## 10. Сборка и поставка

- `go build -trimpath -ldflags "-s -w -X main.version=…" -o yi ./cmd/yi`.
- `Makefile`: `build`, `test`, `lint` (`go vet` + `staticcheck`), `release-snapshot`.
- goreleaser: linux/darwin/windows × amd64/arm64, архивы + checksums (после MVP).
- GitHub Actions: `go test ./...` + `go vet` на push/PR.

## 11. Принципы (из исходной идеи, обязательны)

- `Kanban.md` остаётся человекочитаемым: никаких скрытых id, комментариев, служебных блоков.
- Task note и issue имеют разные роли; body issue не синхронизируется с note.
- Явная синхронизация: `pull` / `push` / `status`. Никакой фоновой магии.
- Issue ID — единственный идентификатор.
- Код провайдера изолирован в его пакете.

## 12. Расширение в будущем

**Новый issue-провайдер (GitLab):** пакет `internal/provider/gitlab` с `init(){ provider.Register("gitlab", New) }`,
blank-import в `cmd/yi/main.go`, прохождение `providertest`. Специфика: `iid` как `ID`,
`add_labels`/`remove_labels`, `state_event: close|reopen`, токен `GITLAB_TOKEN` → `glab auth
token`, `gitlab.baseUrl` для self-hosted. SourceCraft: сначала исследовать API (числовой ли id,
есть ли labels); если labels нет — провайдер может мапить статусы на свой механизм, ядро это
не затрагивает, т. к. оно работает с `Status.Label` как с непрозрачной строкой.

**`yi pr`:** отдельный интерфейс `ChangeRequestProvider` (`CreateChangeRequest(head, base,
title, body)`), регистрируется тем же реестром по отдельности, чтобы провайдер мог
реализовывать только issues.

**`yi sync`:** state file `<project>/.yi/state.json` (`lastRemoteUpdatedAt`, `lastLocalStatus`,
`lastRemoteStatus` по issue). Planner получит третий вход — предыдущее состояние — и сможет
различать local-only / remote-only / conflict. Текущий разрыв Planner/Applier это допускает
без переделки.

**`yi agent`:** читает note через `notes.Store` и формирует prompt; не требует новых
абстракций.

## 13. Принятые решения и допущения (на review)

1. Язык — Go; `git` через exec, не go-git.
2. Статус хранится в labels `status:*`, не в GitHub Projects v2.
3. `pull` импортирует все open issues; закрытые — только уже присутствующие на доске.
4. Создание notes настраивается `notes.create: always | on-demand`, default `always`.
5. Pull переименовывает note при смене title; обратные ссылки в vault не обновляются (warning).
6. Новые и перемещённые карточки вставляются в конец колонки.
7. Ровно один статус с `closeIssue: true`.
8. Wikilink пишется как путь от `<project>` (`[[Tasks/42 Title]]`), как в исходной идее; при
   нескольких проектах в одном vault с одинаковыми путями Obsidian может разрешить ссылку
   неоднозначно — допускаем в MVP.
9. Ветка создаётся от текущего HEAD; настройки base-ветки нет.
