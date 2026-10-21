# `yi` Kanban Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standalone Go CLI `yi`, который синхронизирует `Kanban.md` (Obsidian Kanban) и task notes с GitHub Issues (`status`, `pull`, `push`, `new`, `start`, `done`) через провайдер-агностичное ядро.

**Architecture:** Слоистое ядро: `domain` → (`config`, `kanban`, `notes`, `branch`, `gitrepo`, `provider`) → `reconcile` (чистый Planner + Applier) → `cli`. Провайдеры регистрируются в реестре по имени из конфига и проходят общий conformance suite; ядро не импортирует `provider/github`. `Kanban.md` обрабатывается построчно, всё неизвестное — opaque, round-trip байт-в-байт.

**Tech Stack:** Go ≥1.24, `github.com/spf13/cobra`, `gopkg.in/yaml.v3`, `github.com/google/go-github/v<latest>`, системный `git` через `os/exec`.

**Spec:** [docs/superpowers/specs/2026-10-21-yi-kanban-sync-design.md](../specs/2026-10-21-yi-kanban-sync-design.md) — читать вместе с планом; номера §N ниже ссылаются на него.

## Global Constraints

- Module path: `kanban-sync`; `go.mod`: `go 1.24`. Binary: `yi`, entrypoint `cmd/yi/main.go`.
- `CGO_ENABLED=0`; сборка: `go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o yi ./cmd/yi`.
- Внешние зависимости — только cobra, yaml.v3, go-github (последний мажор: `go list -m -versions github.com/google/go-github/v<N>`; зафиксировать в `go.mod`). Всё остальное — stdlib.
- Правила импорта (§6): `domain` ни от чего; `kanban`, `notes`, `branch`, `gitrepo`, `provider` — только `domain`; `config` — `domain`, `branch`; `reconcile` — `domain`, `config`, `kanban`, `notes`, `provider`; `cli` — всё; только `cmd/yi` импортирует `provider/github`.
- Имя конфига `.issue-sync.yml`; env: `YI_PROJECT`, `GITHUB_TOKEN`, `GH_TOKEN`, `NO_COLOR`, `XDG_CONFIG_HOME`; глобальный конфиг `$XDG_CONFIG_HOME/yi/config.yml` (fallback `~/.config/yi/config.yml`).
- Каноничная карточка: `- [ ] [[Tasks/42 Title]] `#42`` (с note) / `- [ ] Title `#42`` (без note).
- Вывод CLI на английском; цвет только при TTY и пустом `NO_COLOR`.
- Тесты не ходят в сеть. Единственное исключение — opt-in smoke-тест под `YI_E2E_GITHUB_REPO`.
- Любая запись `Kanban.md` — только через `kanban.WriteFileAtomic` с проверкой хеша.
- Каждая задача: `go vet ./...` и `go test ./...` зелёные перед коммитом.

## Review Focus

1. **Карточки с дополнительным содержимым** (`@{2026-10-25}`, `#tag`, многострочные) — move, `[x]`, присвоение id не теряют текст. → Task 5 `TestMovePreservesExtras`, `TestSetIssueIDAppends`.
2. **CRLF и файл без завершающего `\n`** — round-trip идентичен, вставленные строки получают тот же EOL, последняя строка не склеивается. → Task 4 `TestRoundTrip`, Task 5 `TestAddUsesCRLF`, `TestAddAtEOFWithoutNewline`.
3. **Lossy-заголовки** (`Fix: crash`, `a/b`) — нет вечного drift'а; повторный pull/push — пустой план. → Task 9 `TestPullLossyTitleNoDrift`, Task 10 `TestPullTwiceIsNoop`.
4. **Сбой push посередине** — уже созданные id записаны на доску, повтор не плодит issues. → Task 10 `TestPushPartialFailureKeepsIDs`.
5. **Пользователь сохранил `Kanban.md` в Obsidian во время работы** — локальная запись отменяется; для созданных issues печатается подсказка «add `#73` to card». → Task 10 `TestApplyAbortsOnConcurrentEdit`, `TestPushConcurrentEditPrintsCreatedIDs`.

---

### Task 1: Scaffold, domain, `yi version`

**Files:**
- Create: `go.mod`, `.gitignore` (`/yi`, `/dist/`), `Makefile` (`build`, `test`, `lint` = `go vet ./...`), `.github/workflows/ci.yml` (setup-go `stable`, `go vet ./...`, `go test ./...`)
- Create: `cmd/yi/main.go`, `internal/cli/root.go`, `internal/domain/domain.go`
- Test: `internal/domain/domain_test.go`, `internal/cli/root_test.go`

**Interfaces:**
- Produces:
  - `domain.IssueState` (`IssueOpen`, `IssueClosed`), `func (IssueState) String() string` → `"open"`/`"closed"`
  - `domain.Issue{ID int; Title string; State IssueState; Labels []string; URL string; UpdatedAt time.Time}`, `func (Issue) HasLabel(string) bool`
  - `domain.StatusKey string`, `domain.Status{Key StatusKey; Heading, Label string; CloseIssue bool}`
  - `func domain.ParseIssueID(s string) (int, error)`
  - `cli.Deps{Stdin io.Reader; Stdout, Stderr io.Writer; Getenv func(string) string; Getwd func() (string, error); IsTerminal bool; Version string}` (поля для провайдера и git добавят Tasks 11, 14)
  - `func cli.NewRootCommand(d Deps) *cobra.Command` — `SilenceUsage`, `SilenceErrors`, глобальные флаги `--project`, `-v/--verbose`
  - `func cli.Main(d Deps, args []string) int` — exit codes: 0 ok, 1 error (печатает `error: <msg>` в Stderr), 2 usage

- [ ] **Step 1: Write failing tests**

```go
func TestParseIssueID(t *testing.T) {
    ok := map[string]int{"42": 42, "#42": 42, " #7 ": 7}
    for in, want := range ok { /* got == want, err == nil */ }
    for _, bad := range []string{"", "#", "0", "-1", "abc", "#4x"} { /* err != nil */ }
}
func TestIssueStateString(t *testing.T) // IssueOpen.String()=="open", IssueClosed.String()=="closed"
func TestVersionCommand(t *testing.T) {
    // Main(Deps{Version: "1.2.3", Stdout: &out}, []string{"version"}) == 0; out == "yi 1.2.3\n"
}
func TestUnknownCommandExitCode(t *testing.T) // Main(..., []string{"bogus"}) == 2
```

- [ ] **Step 2:** `go test ./...` → FAIL (undefined).
- [ ] **Step 3:** Implement domain + root command. `cmd/yi/main.go`: `var version = "dev"`; `os.Exit(cli.Main(cli.Deps{...os...}, os.Args[1:]))`; `IsTerminal` — `os.Stdout.Stat()` Mode&`os.ModeCharDevice`. Usage-ошибка = ошибка cobra до запуска `RunE` (неизвестная команда/флаг, неверное число аргументов) — различать через обёртку `usageError` в `FlagErrorFunc` и `Args`.
- [ ] **Step 4:** `go test ./... && go vet ./... && make build` → PASS, `./yi version` печатает `yi dev`.
- [ ] **Step 5:** Commit `feat: scaffold yi CLI with domain types and version command`.

---

### Task 2: Config loading and validation

**Files:**
- Create: `internal/config/config.go`, `internal/config/validate.go`
- Test: `internal/config/config_test.go`
- Create (минимально, для валидации шаблона): `internal/branch/template.go` с `ValidateTemplate`

**Interfaces:**
- Consumes: `domain.Status`, `domain.StatusKey`, `domain.Issue`.
- Produces:
  - `config.NotesMode` (`NotesAlways = "always"`, `NotesOnDemand = "on-demand"`)
  - `config.Config{Path, Dir string; Provider, Repository string; ProviderOptions yaml.Node; KanbanPath, TasksDir string; Statuses []domain.Status; DefaultStatus, StartStatus, DoneStatus domain.StatusKey; Notes NotesMode; Git *GitConfig}`
  - `config.GitConfig{Repository, BranchTemplate, DefaultType string}`
  - `func config.Load(path string) (*Config, error)`; `func config.Parse(data []byte, path string) (*Config, error)` (`Dir = filepath.Dir(abs(path))`)
  - `*config.ValidationError{Problems []string}` — `Error()` = `"invalid config <path>:\n  - p1\n  - p2"`
  - методы: `Status(k) (domain.Status, bool)`, `StatusByHeading(h string) (domain.Status, bool)` (trim + `strings.EqualFold`), `CloseStatus() domain.Status`, `StatusLabels() []string`, `StatusOf(is domain.Issue) (domain.StatusKey, []string)` (warnings)
  - `func branch.ValidateTemplate(t string) error` — допустимы только `{type}`, `{issue}`, `{slug}`

- [ ] **Step 1: Write failing tests**

```go
func TestParseFullExample(t *testing.T) {
    // YAML из §4 спеки (с github.baseUrl и git), path = dir+"/.issue-sync.yml"
    // keys(cfg.Statuses) == [backlog todo in-progress completed]
    // cfg.KanbanPath == dir+"/Kanban.md"; cfg.TasksDir == dir+"/Tasks"
    // cfg.Notes == NotesAlways; cfg.DoneStatus == "completed"; cfg.StartStatus == "in-progress"
    // cfg.Git.Repository == home+"/Projects/yandex-ads"; cfg.Git.BranchTemplate == "{type}/{issue}-{slug}"
    // ProviderOptions decodes to struct{BaseURL string `yaml:"baseUrl"`}{"https://github.example.com/api/v3/"}
}
func TestDefaults(t *testing.T) {
    // provider, repository, statuses{a{heading:A,label:l:a}, done{heading:Done,label:l:d,closeIssue:true}}
    // Kanban=Dir/Kanban.md, Tasks=Dir/Tasks, DefaultStatus="a", DoneStatus="done",
    // StartStatus="" (нет ключа in-progress), Notes=always, Git==nil
}
func TestValidationErrors(t *testing.T) {
    // table: name → yaml mutation → substring в Error()
    // "repository is required"; "statuses: at least one status is required";
    // "exactly one status must have closeIssue: true" (0 и 2 таких);
    // "duplicate heading \"Todo\""; "duplicate label \"status:todo\"";
    // "defaultStatus: unknown status \"nope\""; "doneStatus \"todo\" must have closeIssue: true";
    // "notes.create: must be always or on-demand"; "git.branches.template: unknown placeholder {foo}"
    // + все проблемы собираются в один ValidationError (2 ошибки → len(Problems)==2)
}
func TestStatusOf(t *testing.T) {
    // closed + [status:todo]           → completed, no warnings
    // open + [bug, status:todo]        → todo
    // open + []                        → backlog (DefaultStatus)
    // open + [status:in-progress, status:todo] → todo (первый по порядку), 1 warning содержит "#N has multiple status labels"
    // open + [status:completed]        → completed
}
func TestStatusByHeading(t *testing.T) // " in progress " → in-progress; "Nope" → false
```

- [ ] **Step 2:** `go test ./internal/config/` → FAIL.
- [ ] **Step 3:** Implement. Имя провайдера здесь не проверяется (`config` не импортирует реестр) — неизвестный провайдер отвергает `provider.New` при загрузке проекта (Task 11). `statuses` декодировать как `yaml.Node` (MappingNode) и обходить пары по порядку. `~`/`~/` раскрывать через `os.UserHomeDir`; относительные пути — от `Dir`. Секция провайдера: нода по ключу, равному значению `provider`. Default `StartStatus = "in-progress"` только если такой ключ есть. `branch.Defaults`: template `"{type}/{issue}-{slug}"`, type `"feat"` — подставлять, если `git` задан, а поля пусты.
- [ ] **Step 4:** `go test ./internal/config/ ./internal/branch/` → PASS.
- [ ] **Step 5:** Commit `feat(config): load and validate .issue-sync.yml`.

---

### Task 3: Project resolution

**Files:**
- Create: `internal/config/resolve.go`
- Test: `internal/config/resolve_test.go`

**Interfaces:**
- Produces:
  - `config.ResolveInput{Flag, Env, Cwd, GlobalConfigPath string}`
  - `func config.Resolve(in ResolveInput) (string, error)` — путь к `.issue-sync.yml`
  - `func config.GlobalConfigPath(getenv func(string) string) string`
  - `config.FileName = ".issue-sync.yml"`

- [ ] **Step 1: Write failing tests** (всё в `t.TempDir()`)

```go
func TestResolveFlagWins(t *testing.T)     // Flag=dirA (с конфигом), Env=dirB → dirA/.issue-sync.yml
func TestResolveFlagWithoutConfig(t *testing.T) // Flag=пустой dir → err contains "no .issue-sync.yml in"
func TestResolveEnv(t *testing.T)          // Flag="", Env=dirB → dirB/...
func TestResolveWalkUp(t *testing.T)       // конфиг в root, Cwd=root/a/b → root/...
func TestResolveGlobalRegistry(t *testing.T) {
    // global config: projects: [projDir]; projDir конфиг с git.repository: codeDir
    // Cwd = codeDir/sub → projDir/.issue-sync.yml
}
func TestResolveNotFound(t *testing.T) // err перечисляет: "--project", "YI_PROJECT", "searched up from", global path
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement; в реестре проекты, чей конфиг не грузится, пропускаются; совпадение — `filepath.Rel(gitRepo, cwd)` без `..`. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(config): resolve project from flag, env, cwd, or global registry`.

---

### Task 4: Kanban parsing and round-trip

**Files:**
- Create: `internal/kanban/board.go`, `internal/kanban/card.go`
- Create: `internal/kanban/testdata/idea.md` (доска из §1 исходной идеи), `internal/kanban/testdata/plugin.md` (ниже)
- Test: `internal/kanban/parse_test.go`

**Interfaces:**
- Consumes: `domain.Status`, `domain.StatusKey`.
- Produces:
  - `func kanban.Parse(data []byte, statuses []domain.Status) (*Board, error)` — `*DuplicateError{ID int; Lines []int}` при повторе `#id`
  - `func (*Board) Bytes() []byte`; `func (*Board) Cards() []*Card` (порядок документа, только configured-секции); `func (*Board) Find(id int) *Card`; `func (*Board) EOL() string`
  - `kanban.Card{Status domain.StatusKey; IssueID int /*0 = нет*/; Title, Link string; Checked bool; Line int /*1-based в исходнике*/}` + неэкспортируемые строки
- Модель (§6.2): `preamble []string`, `sections []*section{heading string; status domain.StatusKey /*"" = opaque*/; items []item}`, `trailer []string`. `item` — карточка (первая строка + continuation) или raw-строка. Строки хранятся с EOL. Trailer начинается с первой строки `***` или `%% kanban:settings` после начала первой секции.

`testdata/plugin.md`:

````md
---

kanban-plugin: board

---

## Backlog

- [ ] Plain task without issue
- [ ] [[Tasks/73 Creative Preview]] `#73`
	second line of card
	- [ ] nested checklist item


## Ideas

- [ ] Unknown column card `#99`


## Todo



## in progress

- [ ] [[Tasks/42 Yandex OAuth|OAuth]] `#42` @{2026-10-25}


## Completed

- [x] Fix: crash `#31`




***

## Archive

- [x] Old card `#5`

%% kanban:settings
```
{"kanban-plugin":"board","list-collapse":[false,false,false,false]}
```
%%
````

- [ ] **Step 1: Write failing tests**

```go
func TestRoundTrip(t *testing.T) {
    // для idea.md, plugin.md, plugin.md с \n→\r\n, plugin.md без последнего \n:
    // Parse(x).Bytes() == x (bytes.Equal)
}
func TestCardsParsedFromPluginBoard(t *testing.T) {
    // Cards() == []{Status, IssueID, Title, Link, Checked}:
    // {backlog, 0, "Plain task without issue", "", false}
    // {backlog, 73, "Creative Preview", "Tasks/73 Creative Preview", false}
    // {in-progress, 42, "OAuth", "Tasks/42 Yandex OAuth", false}
    // {completed, 31, "Fix: crash", "", true}
    // карточек из Ideas (#99) и Archive (#5) нет; Find(99)==nil
}
func TestCardTitleRules(t *testing.T) {
    // "- [ ] [[Tasks/42 Yandex OAuth]] `#42`" → Title "Yandex OAuth"
    // "- [ ] [[Tasks/Some note]] `#42`"      → Title "Some note" (префикса id нет)
    // "- [X] Title `#1` and `#2`"            → IssueID 2, Title "Title `#1` and", Checked
    // "- [ ] Tagged #42"                     → IssueID 0 (голый #42 — тег)
}
func TestDuplicateIssueID(t *testing.T) // два `#42` в Todo и Completed → *DuplicateError{ID:42, Lines:[n1,n2]}
func TestEOLDetected(t *testing.T)      // CRLF-вариант → EOL()=="\r\n"; иначе "\n"
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. Карточка: `^- \[( |x|X)\] ` в колонке 0; continuation — следующие непустые строки, начинающиеся с пробела/таба. id: последнее совпадение `` `#(\d+)` ``. Link: `\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(kanban): line-preserving board parser`.

---

### Task 5: Kanban mutations and atomic file IO

**Files:**
- Create: `internal/kanban/mutate.go`, `internal/kanban/file.go`
- Create: `internal/kanban/testdata/*.golden.md` (ожидаемые выходы)
- Test: `internal/kanban/mutate_test.go`, `internal/kanban/file_test.go`

**Interfaces:**
- Produces:
  - `func (*Board) Move(c *Card, to domain.StatusKey) error`
  - `func (*Board) Add(to domain.StatusKey, line string) (*Card, error)` — `line` без EOL, должен парситься как карточка
  - `func (*Board) SetChecked(c *Card, v bool)`
  - `func (*Board) SetIssueID(c *Card, id int)` — дописывает `` ` `#id` `` в конец первой строки
  - `func (*Board) SetLink(c *Card, target string)` — заменяет target в существующем `[[...]]`, alias сохраняется
  - `func (*Board) RewriteFirstLine(c *Card, line string) error` — переразбирает поля карточки
  - `func kanban.FormatCard(checked bool, title, link string, id int) string` — каноничная строка (§6.2)
  - `func kanban.ReadFile(path string) ([]byte, [32]byte, error)`; `func kanban.WriteFileAtomic(path string, data []byte, expect [32]byte) error`; `var kanban.ErrModified`
- Неизвестный `to` (нет в statuses) → ошибка `unknown status "x"`.

- [ ] **Step 1: Write failing tests** (вход — `plugin.md`, сравнение с golden)

```go
func TestMoveToEmptyColumn(t *testing.T)       // #73 → todo: карточка с 2 continuation-строками сразу после "## Todo\n\n"; Backlog без неё; остальное байт-в-байт (golden move_empty.golden.md)
func TestMoveAppendsAfterLastCard(t *testing.T) // Plain → completed: после "- [x] Fix: crash `#31`", до "***" (golden)
func TestMovePreservesExtras(t *testing.T)      // #42 → todo: строка с "|OAuth" и "@{2026-10-25}" идентична исходной
func TestAddCreatesMissingSection(t *testing.T) // statuses + review(heading "Review") между in-progress и completed; Add(review, ...) → "## Review\n\n- [ ] X `#8`\n\n" перед "## Completed" (golden)
func TestAddUsesCRLF(t *testing.T)              // CRLF-доска: добавленная строка оканчивается "\r\n"
func TestAddAtEOFWithoutNewline(t *testing.T)   // "## Todo\n\n- [ ] a `#1`" (без \n) + Add(todo,"- [ ] b `#2`") → "...`#1`\n- [ ] b `#2`"
func TestSetChecked(t *testing.T)               // меняется только символ в "[ ]"
func TestSetIssueIDAppends(t *testing.T)        // "- [ ] Plain task without issue" → "- [ ] Plain task without issue `#80`"; c.IssueID==80; Find(80)==c
func TestSetLinkKeepsAlias(t *testing.T)        // #42: SetLink(c,"Tasks/42 New") → "[[Tasks/42 New|OAuth]]", хвост строки не тронут
func TestFormatCard(t *testing.T) {
    // (false,"Yandex OAuth","Tasks/42 Yandex OAuth",42) → "- [ ] [[Tasks/42 Yandex OAuth]] `#42`"
    // (true,"Fix crash","",31) → "- [x] Fix crash `#31`"
}
func TestWriteFileAtomic(t *testing.T) {
    // ReadFile → WriteFileAtomic(new, hash) ок, содержимое = new, mode 0640 сохранён
    // файл изменён между Read и Write → errors.Is(err, ErrModified), содержимое = чужая правка
}
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. Точка вставки (§6.2): после последней карточки секции; если карточек нет — после первой пустой строки за heading (если её нет — вставить пустую строку, затем карточку). Если предыдущий item не заканчивается EOL — дописать EOL перед вставкой. Новая секция: после ближайшей предыдущей по порядку статусов configured-секции, иначе перед первой configured, иначе в конец секций; формат `## H` + EOL + EOL + карточка + EOL + EOL. Atomic write: temp в том же каталоге, `Chmod` по исходному mode, `Sync`, `Rename`; хеш перечитывается непосредственно перед `Rename`. **Step 4:** PASS (`go test ./internal/kanban/`; golden обновлять флагом `-update`).
- [ ] **Step 5:** Commit `feat(kanban): card mutations and atomic board writes`.

---

### Task 6: Task notes

**Files:**
- Create: `internal/notes/notes.go`
- Test: `internal/notes/notes_test.go`

**Interfaces:**
- Produces:
  - `func notes.Sanitize(title string) string` (§6.3)
  - `notes.Store{ProjectDir, TasksDir string}`; `func notes.NewStore(projectDir, tasksDir string) *Store`
  - `func (*Store) LoadIndex() (*Index, error)` — рекурсивно по `TasksDir`: `issue:` из frontmatter, иначе префикс имени `<id> `; frontmatter приоритетнее
  - `func (*Store) Resolve(idx *Index, id int, link string) (path string, ok bool)` — порядок §6.3
  - `func (*Store) Create(idx *Index, id int, title string) (path string, err error)` — если уже есть (по Resolve) — вернуть существующий путь, не перезаписывать; создаёт `TasksDir`; добавляет в idx
  - `func (*Store) Rename(idx *Index, path string, id int, oldTitle, newTitle string) (newPath string, err error)` — H1 меняется, только если равен `oldTitle`
  - `func (*Store) LinkTarget(path string) string` — относительно `ProjectDir`, без `.md`, через `/`

- [ ] **Step 1: Write failing tests**

```go
func TestSanitize(t *testing.T) {
    // символы удаляются, затем пробелы схлопываются и trim:
    // "Fix: crash" → "Fix crash"; "a/b" → "ab"; "x [y] #z" → "x y z"; "Fix:  crash  " → "Fix crash"
    // 150×"x" → len 100; "Добавить preview" без изменений
}
func TestCreateUsesTemplate(t *testing.T) // Create(idx,73,"Creative Preview") → Tasks/73 Creative Preview.md == шаблон §6.3 побайтно
func TestCreateDoesNotOverwrite(t *testing.T) // существующий файл с issue: 73 и другим именем → возвращён его путь, содержимое не изменено
func TestResolveOrder(t *testing.T) // link на существующий файл > frontmatter issue > префикс имени; link на несуществующий → fallback
func TestRenameUpdatesH1(t *testing.T) // H1 "# Old" → "# New"; файл "42 New.md"; старого нет
func TestRenameKeepsCustomH1(t *testing.T) // H1 "# My heading" не трогается
func TestLinkTarget(t *testing.T) // ProjectDir/Tasks/42 X.md → "Tasks/42 X"
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement (frontmatter — только строки между первыми `---`, ключ `issue:` int; YAML-парсер не нужен). **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(notes): task note index, creation and rename`.

---

### Task 7: Provider interface, registry, fake, conformance suite

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/registry.go`
- Create: `internal/provider/fake/fake.go`
- Create: `internal/provider/providertest/suite.go`
- Test: `internal/provider/registry_test.go`, `internal/provider/fake/fake_test.go`

**Interfaces:**
- Produces:
  - `provider.IssueProvider`, `NewIssue`, `IssueUpdate`, `Config{Name, Repository string; Options yaml.Node}`, `Factory`, `ErrNotFound`, `ErrAuth` — ровно как §6.1
  - `func provider.Register(name string, f Factory)` (panic на дубль), `func provider.New(ctx, Config) (IssueProvider, error)` (неизвестное имя → `unknown provider "x" (available: github)`), `func provider.Names() []string` (sorted)
  - `fake.Provider`: `func fake.New() *Provider`; `Seed(issues ...domain.Issue)`; `Issue(id) domain.Issue`; `Calls() []string` (`"list"`, `"get 42"`, `"create Title"`, `"update 42"`); `FailAfter(n int, err error)` — n успешных мутаций (`create`/`update`), затем каждая мутация возвращает err. Новые id: max(seeded)+1. URL: `https://fake/issues/<id>`.
  - `func providertest.Run(t *testing.T, newProvider func(t *testing.T) provider.IssueProvider)`

- [ ] **Step 1: Write failing tests**

```go
// providertest.Run subtests (используют только интерфейс):
// "CreateGet": Create{Title:"A", Labels:[status:todo]} → Get(id): Title "A", State open, HasLabel(status:todo), URL != ""
// "ListOpenExcludesClosed": create 2, close 1 via Update{State:&closed} → List содержит только открытый
// "LabelsAddRemove": labels [bug,status:todo] → Update{Add:[status:in-progress], Remove:[status:todo]} → labels {bug,status:in-progress}
// "RemoveAbsentLabel": Update{Remove:[nope]} → no error
// "CloseReopen": close → State closed; reopen → open
// "UpdateTitle": Update{Title:&"B"} → Get.Title=="B", labels не изменились
// "NotFound": Get(99999) и Update(99999,…) → errors.Is(err, ErrNotFound)
func TestFakeConformance(t *testing.T) { providertest.Run(t, func(*testing.T) provider.IssueProvider { return fake.New() }) }
func TestFakeFailAfter(t *testing.T) // FailAfter(1, boom): 1-й create ок, 2-й → boom
func TestRegistry(t *testing.T)      // Register("x", f); New(Config{Name:"x"}) вызывает f; New(Config{Name:"nope"}) → err contains `unknown provider "nope"`
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement (fake — mutex + map, возвращает копии). **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(provider): provider registry, fake provider and conformance suite`.

---

### Task 8: GitHub provider

**Files:**
- Create: `internal/provider/github/github.go`, `internal/provider/github/auth.go`
- Create: `internal/provider/github/fakeserver_test.go` (in-memory GitHub REST эмулятор на `httptest`)
- Test: `internal/provider/github/github_test.go`, `internal/provider/github/auth_test.go`
- Modify: `cmd/yi/main.go` — `import _ "kanban-sync/internal/provider/github"`

**Interfaces:**
- Consumes: `provider.*`, `providertest.Run`.
- Produces: регистрация `"github"`; `func New(ctx context.Context, cfg provider.Config) (provider.IssueProvider, error)`; для тестов `func newWithClient(hc *http.Client, baseURL, repo string) (*Provider, error)`; `func resolveToken(ctx context.Context, getenv func(string) string, lookPath func(string) (string, error), run func(ctx context.Context, name string, args ...string) ([]byte, error)) (string, error)`.
- Эмулятор обслуживает: `GET/POST /repos/{o}/{r}/issues` (state, per_page, page + `Link` header), `GET/PATCH /repos/{o}/{r}/issues/{n}`, `POST /repos/{o}/{r}/issues/{n}/labels`, `DELETE /repos/{o}/{r}/issues/{n}/labels/{name}` (404 если нет), умеет сидировать PR (`pull_request` поле) и отвечать 401.

- [ ] **Step 1: Write failing tests**

```go
func TestGitHubConformance(t *testing.T)   // providertest.Run поверх эмулятора
func TestListFiltersPullRequests(t *testing.T) // 2 issue + 1 PR → 2
func TestListPaginates(t *testing.T)       // 230 open issues, per_page 100 → 230, 3 запроса страниц
func TestUnauthorized(t *testing.T)        // 401 → errors.Is(err, provider.ErrAuth)
func TestRepositoryFormat(t *testing.T)    // Repository "noslash" → err contains "owner/repo"
func TestBaseURLOption(t *testing.T)       // Options baseUrl → запросы идут на этот хост
func TestResolveToken(t *testing.T) {
    // GITHUB_TOKEN=a → "a"; только GH_TOKEN=b → "b";
    // нет env, lookPath ok, run → "c\n" → "c"; нет env, lookPath err →
    // errors.Is(err, provider.ErrAuth) && contains "GITHUB_TOKEN" && contains "gh auth login"
}
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement (§6.1 GitHub). Статусы 401/403 → `ErrAuth`; 404/410 → `ErrNotFound`; оборачивать `fmt.Errorf("github: get issue #%d: %w", ...)`. Rate limit (`*github.RateLimitError`) → `github: rate limit exceeded, resets at <time>`. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(github): GitHub issue provider`.

---

### Task 9: Reconcile planner and remote fetch

**Files:**
- Create: `internal/reconcile/changes.go`, `internal/reconcile/plan.go`, `internal/reconcile/status.go`, `internal/reconcile/fetch.go`
- Test: `internal/reconcile/plan_test.go`, `internal/reconcile/fetch_test.go`

**Interfaces:**
- Consumes: `config.Config` (+`StatusOf`, `CloseStatus`, `StatusLabels`), `kanban.Board/Card`, `notes.Sanitize`, `provider.IssueProvider`, `provider.IssueUpdate`.
- Produces:
  - `reconcile.Input{Cfg *config.Config; Board *kanban.Board; Remote map[int]domain.Issue; Missing map[int]bool; HasNote func(*kanban.Card) bool}`
  - `reconcile.Plan{Changes []Change; Warnings []string}`; `func (Plan) Empty() bool`
  - Change-типы (§6.6, все с методом `isChange()`): `MoveCard{Card; From, To}`, `AddCard{Issue; To; WithNote bool}`, `RenameLocal{Card; ID int; From, To string}`, `LinkNote{Card; ID int; Title string}`, `CreateIssue{Card; Title, Body string; Status domain.StatusKey; WithNote bool}`, `SetStatus{ID int; From, To domain.StatusKey; Update provider.IssueUpdate}`, `RenameRemote{ID int; From, To string}`
  - `func reconcile.PlanPull(in Input) Plan`, `func reconcile.PlanPush(in Input) Plan`
  - `func reconcile.RemoteUpdateFor(cfg *config.Config, is domain.Issue, to domain.StatusKey) (provider.IssueUpdate, bool)` — labels: add label `to`, если нет; remove прочие status-labels; state: close/reopen по `CloseIssue`; `false`, если ничего не нужно
  - `reconcile.RowKind` (`RowOK`, `RowDiff`, `RowLocalOnly`, `RowRemoteOnly`, `RowMissing`); `reconcile.Row{Kind RowKind; ID int; Title string; Local, Remote domain.StatusKey; TitleDiff bool}`; `func reconcile.Status(in Input) ([]Row, []string)`
  - `func reconcile.Describe(cfg *config.Config, c Change) string`
  - `func reconcile.FetchRemote(ctx context.Context, p provider.IssueProvider, b *kanban.Board) (remote map[int]domain.Issue, missing map[int]bool, err error)` — list open + `GetIssue` для id доски вне списка, ≤8 параллельно; `ErrNotFound` → missing
- Порядок изменений детерминирован: карточки в порядке документа; `AddCard` — по возрастанию id.
- Title: сравнение `notes.Sanitize(remote) != notes.Sanitize(card.Title)` (§6.4).

- [ ] **Step 1: Write failing tests** (доска строится из строк через `kanban.Parse`, конфиг — 4 статуса из §4, `HasNote` из map)

```go
func TestPlanPullMovesCard(t *testing.T)         // #57 local todo, remote open+status:in-progress → [MoveCard{#57, todo→in-progress}]
func TestPlanPullClosedToCompleted(t *testing.T) // #42 in-progress, remote closed → MoveCard → completed
func TestPlanPullAddsUnknownOpen(t *testing.T)   // remote #80 open без labels, нет на доске → AddCard{#80, backlog, WithNote: true}; notes on-demand → WithNote false
func TestPlanPullIgnoresUnknownClosed(t *testing.T) // closed #81 не на доске (не в Remote, т.к. не загружен) → нет изменений
func TestPlanPullRename(t *testing.T)            // card "Yandex OAuth", remote "Yandex OAuth 2.0" → RenameLocal{From:"Yandex OAuth", To:"Yandex OAuth 2.0"}
func TestPullLossyTitleNoDrift(t *testing.T)     // card [[Tasks/31 Fix crash]], remote "Fix: crash" → нет RenameLocal
func TestPlanPullLinkNote(t *testing.T)          // always + карточка без note → LinkNote; on-demand → нет
func TestPlanPullMissingWarns(t *testing.T)      // Missing{99} → warning contains "#99 not found", карточка без изменений
func TestPlanPullSkipsCardsWithoutID(t *testing.T)
func TestPlanPushStatusLabels(t *testing.T) {
    // #57 local in-progress, remote open [bug, status:todo] →
    // SetStatus{57, todo→in-progress, Update{AddLabels:[status:in-progress], RemoveLabels:[status:todo]}}; State == nil
}
func TestPlanPushClose(t *testing.T)    // local completed, remote open [status:in-progress] → Update.State = closed, Add [status:completed]
func TestPlanPushReopen(t *testing.T)   // local todo, remote closed [status:completed] → State open, Add todo, Remove completed
func TestPlanPushCreate(t *testing.T)   // "- [ ] New thing" в todo → CreateIssue{Title:"New thing", Status:todo, WithNote:true}
func TestPlanPushRenameRemote(t *testing.T) // [[Tasks/42 OAuth v2]] vs remote "OAuth" → RenameRemote{42,"OAuth","OAuth v2"}
func TestPlanPushIgnoresRemoteOnly(t *testing.T)
func TestStatusRows(t *testing.T) // OK, Diff (статус), Diff (TitleDiff), LocalOnly, RemoteOnly, Missing — по одной строке каждого вида
func TestDescribe(t *testing.T)   // SetStatus todo→completed с close → "#57 status: Todo → Completed, close issue"; CreateIssue → `create issue "New thing" in Todo`
func TestFetchRemote(t *testing.T) // fake: open #1, closed #2, на доске #1 #2 #3 → remote{1,2}, missing{3}; Calls содержит "get 2","get 3", не "get 1"
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(reconcile): pull/push/status planner and remote fetch`.

---

### Task 10: Applier

**Files:**
- Create: `internal/reconcile/apply.go`
- Test: `internal/reconcile/apply_test.go`

**Interfaces:**
- Consumes: Task 5 (`Move`, `Add`, `SetChecked`, `SetIssueID`, `SetLink`, `RewriteFirstLine`, `FormatCard`, `WriteFileAtomic`, `ErrModified`), Task 6 (`Store`, `Index`), Task 9 (changes, `RemoteUpdateFor`).
- Produces:
  - `reconcile.Applier{Cfg *config.Config; Board *kanban.Board; BoardHash [32]byte; Provider provider.IssueProvider; Notes *notes.Store; Index *notes.Index; Log io.Writer}`
  - `func (*Applier) Apply(ctx context.Context, p Plan) error`
  - `func reconcile.MoveCard(cfg *config.Config, b *kanban.Board, c *kanban.Card, to domain.StatusKey) error` — `Move` + checkbox: в `CloseIssue`-статус → `[x]`, из него → `[ ]`
- Семантика (§6.6): изменения применяются по порядку; `AddCard`: note (если `WithNote`) + `FormatCard(to.CloseIssue, …)`; `LinkNote`: `Create` + `RewriteFirstLine(FormatCard(c.Checked, title, link, id))`; `RenameLocal`: если есть note — `Rename` + `SetLink`, иначе `RewriteFirstLine`; печатать warning `other links to "<old>" were not updated`; `CreateIssue`: `provider.CreateIssue{Labels:[label]}` → если `CloseIssue` — `UpdateIssue{State: closed}` → если `WithNote`: note + `RewriteFirstLine` с link, иначе `SetIssueID`. Каждое применённое изменение → строка `✓ <Describe>` в `Log`. При ошибке remote — остановиться, сохранить доску, вернуть `fmt.Errorf("applied %d of %d changes: %w", …)`. Доска пишется, только если `Bytes()` отличается от исходного хеша. При `ErrModified` — для каждого созданного в этом прогоне issue печатать `add "#73" to card "Title" manually` и вернуть ошибку `Kanban.md was modified during sync; re-run`.

- [ ] **Step 1: Write failing tests** (temp-проект с `Kanban.md` и `Tasks/`, `fake.Provider`)

```go
func TestApplyPullEndToEnd(t *testing.T)   // remote #80 todo + #57 закрыт → после Apply: Tasks/80 ….md есть, карточки на месте, #57 в Completed с [x]
func TestPullTwiceIsNoop(t *testing.T)     // повторный PlanPull после Apply (перечитанная доска) → Empty(); то же с remote title "Fix: crash"
func TestPushTwiceIsNoop(t *testing.T)
func TestApplyCreateIssueWritesID(t *testing.T) // "- [ ] New thing" → fake получил create с [status:todo]; карточка "- [ ] [[Tasks/N New thing]] `#N`"
func TestApplyCreateInCompletedCloses(t *testing.T)
func TestPushPartialFailureKeepsIDs(t *testing.T) {
    // 3 новые карточки, FailAfter(2) (create #1, create #2 ок, третий падает)
    // Apply → err contains "applied 2 of 3"; на диске у двух карточек есть `#id`;
    // повторный push с fake без ошибок создаёт ровно 1 issue (всего 3)
}
func TestApplyAbortsOnConcurrentEdit(t *testing.T) // между Parse и Apply файл переписан → errors.Is(err, kanban.ErrModified), чужая правка цела
func TestPushConcurrentEditPrintsCreatedIDs(t *testing.T) // Log содержит `add "#N" to card "New thing" manually`
func TestMoveCardCheckbox(t *testing.T)    // todo→completed: [x]; completed→todo: [ ]
func TestRenameLocalRenamesNote(t *testing.T) // файл переименован, link на карточке обновлён, alias сохранён, warning в Log
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(reconcile): applier with atomic writes and partial-failure recovery`.

---

### Task 11: CLI `status`, `pull`, `push`

**Files:**
- Create: `internal/cli/project.go`, `internal/cli/status.go`, `internal/cli/pull.go`, `internal/cli/push.go`, `internal/cli/render.go`
- Modify: `internal/cli/root.go`, `cmd/yi/main.go`
- Test: `internal/cli/sync_test.go`

**Interfaces:**
- Consumes: Tasks 2–10.
- Produces:
  - `cli.Deps` + поле `NewProvider func(context.Context, provider.Config) (provider.IssueProvider, error)` (main: `provider.New`)
  - `type project struct{ cfg *config.Config; board *kanban.Board; hash [32]byte; store *notes.Store; index *notes.Index; prov provider.IssueProvider }`; `func loadProject(ctx context.Context, d Deps, flag string) (*project, error)` — Resolve → Load → ReadFile → Parse → LoadIndex → NewProvider
  - `func (p *project) input(remote, missing) reconcile.Input`; `func (p *project) applier(log io.Writer) *reconcile.Applier`
  - `func renderStatus(w io.Writer, cfg *config.Config, rows []reconcile.Row, warns []string, color bool)` — `text/tabwriter`, колонки `Issue  Local  Remote  Title  State` (Title — remote title; при `TitleDiff` — `local → remote`), значок `✓` / `!` / `+local` / `+remote` / `?` (§6.6); local/remote печатаются heading'ами
  - `func renderPlan(w io.Writer, cfg *config.Config, p reconcile.Plan)` — `Changes:` + по строке `  <Describe>`; пустой план → `No changes.`; warnings — `warning: <msg>` в конце
- Тексты: подтверждение `Apply %d changes? [y/N] `; без TTY и `--yes` → ошибка `refusing to push without confirmation; re-run with --yes`; ответ не `y`/`yes` → `Aborted.` и exit 0.

- [ ] **Step 1: Write failing tests** (`Main` с `Deps{NewProvider: возвращает общий fake, Getwd: projectDir, IsTerminal: false}`)

```go
func TestStatusCommand(t *testing.T)   // stdout содержит "#57", "Todo", "In Progress", "!" и "+remote"; файлы не изменены
func TestPullCommand(t *testing.T)     // exit 0; stdout "Changes:"; Kanban.md содержит "[[Tasks/80 "; второй запуск → "No changes."
func TestPullDryRun(t *testing.T)      // файлы не изменены, stdout содержит план
func TestPushRequiresYesWithoutTTY(t *testing.T) // exit 1, stderr "refusing to push without confirmation"; fake.Calls без create/update
func TestPushYes(t *testing.T)         // --yes → fake создал issue, на доске `#N`
func TestPushConfirmPrompt(t *testing.T) // IsTerminal: true, Stdin "n\n" → "Aborted.", exit 0, без мутаций
func TestNoConfig(t *testing.T)        // пустой cwd → exit 1, stderr contains ".issue-sync.yml"
func TestDuplicateCardsError(t *testing.T) // exit 1, stderr contains "#42" и номера строк
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement; `-v` — логирование в Stderr (`provider.Config`/HTTP-трейс не обязателен: достаточно печати загруженного конфига и числа issues). `signal.NotifyContext(os.Interrupt)` — в `cmd/yi/main.go`, контекст передаётся через `cmd.ExecuteContext`. **Step 4:** PASS. Ручная проверка: `make build && ./yi --project <tmp> status` с `GITHUB_TOKEN` на тестовом репо (опционально).
- [ ] **Step 5:** Commit `feat(cli): status, pull and push commands`.

---

### Task 12: Branch names

**Files:**
- Create: `internal/branch/branch.go`, `internal/branch/translit.go`
- Modify: `internal/branch/template.go` (из Task 2)
- Test: `internal/branch/branch_test.go`

**Interfaces:**
- Produces: `func branch.Slug(title string) string`; `func branch.Name(template, typ string, id int, title string) (string, error)`.

- [ ] **Step 1: Write failing tests**

```go
func TestSlug(t *testing.T) {
    // "Yandex OAuth" → "yandex-oauth"
    // "Добавить preview generated creative" → "dobavit-preview-generated-creative"
    // "Щука и ёж" → "shchuka-i-yozh"; "Объект" → "obekt"
    // "Fix: crash!!  (urgent)" → "fix-crash-urgent"
    // "!!!" → ""; 80 символов "word-…" → ≤50, не заканчивается на "-", обрезан по границе слова
}
func TestName(t *testing.T) {
    // ("{type}/{issue}-{slug}","feat",42,"Yandex OAuth") → "feat/42-yandex-oauth"
    // ("{type}/{issue}-{slug}","fix",7,"!!!") → "fix/7"
    // ("{issue}","feat",3,"x") → "3"
    // ("{type}/{nope}",...) → error
}
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. Таблица транслитерации — §6.8 (`а a, б b, в v, г g, д d, е e, ё yo, ж zh, з z, и i, й j, к k, л l, м m, н n, о o, п p, р r, с s, т t, у u, ф f, х kh, ц c, ч ch, ш sh, щ shch, ъ "", ы y, ь "", э e, ю yu, я ya`). После подстановки — trim висящих `-` и `/`. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(branch): branch name templates with transliterated slugs`.

---

### Task 13: Git repository adapter

**Files:**
- Create: `internal/gitrepo/gitrepo.go`
- Test: `internal/gitrepo/gitrepo_test.go`

**Interfaces:**
- Produces: `gitrepo.Repository` interface (§6.9) + `CheckBranchName(ctx, name string) error`; `func gitrepo.Open(dir string) Repository`; ошибки вида `git switch foo: <stderr trimmed>`.

- [ ] **Step 1: Write failing tests** (`t.Skip`, если `exec.LookPath("git")` неуспешен; temp-репо: `git init -b main`, `user.email/name`, пустой коммит)

```go
func TestCurrentBranch(t *testing.T)    // "main"
func TestCreateAndSwitch(t *testing.T)  // CreateAndSwitch("feat/1-x") → CurrentBranch == "feat/1-x"; BranchExists == true
func TestSwitchExisting(t *testing.T)
func TestBranchExistsFalse(t *testing.T)
func TestCheckBranchName(t *testing.T)  // "feat/1-x" ok; "bad..name" → error
func TestGitErrorIncludesStderr(t *testing.T) // Switch("nope") → err contains "nope"
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement через `exec.CommandContext("git", "-C", dir, …)`; `BranchExists` — `git rev-parse --verify --quiet refs/heads/<name>` (exit 1 → false). **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(gitrepo): exec-based git adapter`.

---

### Task 14: CLI `new`, `start`, `done`

**Files:**
- Create: `internal/cli/new.go`, `internal/cli/start.go`, `internal/cli/done.go`
- Modify: `internal/cli/root.go`, `cmd/yi/main.go`
- Test: `internal/cli/workflow_test.go`

**Interfaces:**
- Consumes: `reconcile.Applier`, change-типы, `RemoteUpdateFor`, `branch.Name`, `gitrepo.Repository`.
- Produces: `cli.Deps` + поле `OpenGit func(dir string) gitrepo.Repository` (main: `gitrepo.Open`).
- Все три команды строят `reconcile.Plan` из готовых change-типов и применяют `Applier` (без подтверждения — команда сама явный запрос):
  - `new`: `board.Add(status, FormatCard(false, title, "", 0))` → `Plan{CreateIssue{Card, Title, Body, Status, WithNote: true}}`; вывод `✓ Created #N <url>` и `✓ Note Tasks/N Title.md`.
  - `start`: `GetIssue`; карточка есть → `MoveCard` (если статус другой), нет → `AddCard{WithNote: true}`; нет note → `LinkNote`; `RemoteUpdateFor(issue, StartStatus)` → `SetStatus`; затем git (§6.7). Пустой `StartStatus` → ошибка `startStatus is not configured`. Нет `git` в конфиге или `--no-branch` → git пропускается. Ошибка git → `error: …` + строка `local and remote changes were applied`.
  - `done`: карточка обязательна (`#N is not on the board`); `MoveCard` → `DoneStatus` + `SetStatus` с close.

- [ ] **Step 1: Write failing tests** (fake provider; для `start` — реальный temp git-репо, skip без git)

```go
func TestNewCommand(t *testing.T)       // `new "Creative Preview" --status todo` → fake issue с [status:todo]; Tasks/N Creative Preview.md; карточка в Todo
func TestNewDefaultStatus(t *testing.T) // без --status → backlog
func TestStartCommand(t *testing.T) {
    // #42 в Todo, closed=false; git.repository = tmp repo
    // `start #42` → карточка в In Progress; fake labels {status:in-progress}; note есть;
    // CurrentBranch == "feat/42-yandex-oauth"; stdout содержит "✓ Switched to feat/42-yandex-oauth"
}
func TestStartExistingBranch(t *testing.T) // ветка уже есть → switch, не create
func TestStartNotOnBoard(t *testing.T)     // issue только в remote → карточка добавлена в In Progress с note
func TestStartType(t *testing.T)           // --type fix → "fix/42-…"
func TestStartNoBranch(t *testing.T)       // --no-branch → ветка не меняется
func TestStartReopensClosed(t *testing.T)
func TestDoneCommand(t *testing.T)         // `done 42` → Completed + [x]; fake: closed, labels {status:completed}
func TestDoneNotOnBoard(t *testing.T)      // exit 1, "#42 is not on the board"
```

- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS; `go test ./... && go vet ./...`.
- [ ] **Step 5:** Commit `feat(cli): new, start and done workflow commands`.

---

### Task 15: Release tooling, docs, smoke test

**Files:**
- Create: `.goreleaser.yaml` (builds: `./cmd/yi`, `CGO_ENABLED=0`, goos linux/darwin/windows, goarch amd64/arm64, ldflags `-s -w -X main.version={{.Version}}`; archives + checksums)
- Create: `README.md` — установка (`go install kanban-sync/cmd/yi@latest` — только после переноса module path на реальный репозиторий, иначе `make build`), пример `.issue-sync.yml` (§4), auth (§6.1), команды (§7), раздел «Adding a provider» (§12: пакет, `Register`, blank-import, `providertest.Run`)
- Create: `internal/provider/github/smoke_test.go` (build tag отсутствует; `t.Skip` без `YI_E2E_GITHUB_REPO`)
- Modify: `Makefile` — `release-snapshot: goreleaser release --snapshot --clean`

- [ ] **Step 1: Write smoke test** — `providertest.Run` против реального репо из `YI_E2E_GITHUB_REPO` с токеном из `resolveToken`; созданные issues закрываются в `t.Cleanup`.
- [ ] **Step 2:** `go test ./...` → smoke пропущен (SKIP), остальное PASS.
- [ ] **Step 3:** Кросс-сборка: `for os in linux darwin windows; do GOOS=$os GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/yi; done` → без ошибок. `file yi` → `statically linked`.
- [ ] **Step 4:** Commit `chore: release config, README and GitHub smoke test`.
