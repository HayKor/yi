# Obsidian Issue Sync CLI

CLI для связывания локального Kanban в Obsidian с Issues в GitHub, GitLab и SourceCraft.

## Цель

Использовать Obsidian как основной локальный интерфейс для работы с задачами:

```text
Obsidian Kanban
      ↕
     CLI
      ↕
GitHub / GitLab / SourceCraft Issues
      ↕
Git branches / Pull Requests
```

Основные сущности:

- `Kanban.md` — обзор текущих задач и их состояния.
    
- `Tasks/*.md` — подробный локальный контекст задачи.
    
- Remote Issue — командная задача.
    
- Git branch — реализация задачи.
    
- Pull Request / Merge Request — завершение реализации.
    

---

# 1. Структура проекта в Obsidian

```text
~/Obsidian/Projects/yandexAds/
├── Kanban.md
├── Project.md
├── .issue-sync.yml
└── Tasks/
    ├── 42 Yandex OAuth.md
    ├── 57 Analytics API.md
    └── 73 Creative Preview.md
```

## Kanban.md

```md
## Backlog

- [ ] [[Tasks/73 Creative Preview]] `#73`

## Todo

- [ ] [[Tasks/57 Analytics API]] `#57`

## In Progress

- [ ] [[Tasks/42 Yandex OAuth]] `#42`

## Completed

- [x] [[Tasks/31 AppTheme]] `#31`
```

Kanban должен оставаться обычным человекочитаемым Markdown без большого количества технической metadata.

---

# 2. Task notes

Каждой достаточно крупной задаче соответствует отдельная Obsidian note.

Пример:

```md
---
issue: 42
---

# Yandex OAuth

## Context

Необходимо реализовать OAuth авторизацию через Yandex.

Android callback:

`konstruktor://auth/callback`

## Implementation plan

- [x] Обработка deep link
- [ ] Exchange authorization code
- [ ] Error handling
- [ ] Проверить logout/login flow

## Notes

Дополнительный технический контекст задачи.
```

Task note предназначена прежде всего для локальной работы.

В ней можно хранить:

- implementation plan;
    
- технический контекст;
    
- ссылки на нужные файлы;
    
- архитектурные решения;
    
- research;
    
- заметки;
    
- checklist;
    
- контекст для Codex/другого coding agent.
    

Issue body не должен автоматически полностью синхронизироваться с содержимым Task note.

---

# 3. Конфигурация проекта

В корне Obsidian-проекта:

```text
.issue-sync.yml
```

Пример:

```yaml
provider: github

repository: my-org/yandex-ads

kanban: ./Kanban.md
tasks: ./Tasks

statuses:
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

git:
  repository: ~/Projects/yandex-ads

  branches:
    template: "{type}/{issue}-{slug}"
    defaultType: feat
```

Для GitLab:

```yaml
provider: gitlab
repository: group/yandex-ads
```

Для SourceCraft:

```yaml
provider: sourcecraft
repository: ...
```

---

# 4. Модель статусов

Локальные состояния:

```text
Backlog
Todo
In Progress
Completed
```

Remote mapping:

|Local|Remote|
|---|---|
|Backlog|`status:backlog`, issue open|
|Todo|`status:todo`, issue open|
|In Progress|`status:in-progress`, issue open|
|Completed|`status:completed`, issue closed|

Issue `open/closed` не используется как единственный статус.

`closed` означает только фактическое завершение задачи.

---

# 5. Идентификатор задачи

Remote Issue ID является главным идентификатором задачи.

Например:

```text
Issue #42
        ↓
Tasks/42 Yandex OAuth.md
        ↓
feat/42-yandex-oauth
        ↓
Pull Request
        ↓
Closes #42
```

Не вводить отдельные `TASK-123`, если remote issue уже существует.

---

# 6. Архитектура CLI

CLI не должен зависеть напрямую от конкретного issue tracker.

Основная abstraction:

```text
IssueProvider
├── GitHubIssueProvider
├── GitLabIssueProvider
└── SourceCraftIssueProvider
```

Пример интерфейса:

```kotlin
interface IssueProvider {

    suspend fun getIssue(id: Int): Issue

    suspend fun getIssues(): List<Issue>

    suspend fun createIssue(
        title: String,
        body: String?
    ): Issue

    suspend fun updateIssue(
        id: Int,
        title: String? = null,
        labels: Set<String>? = null
    )

    suspend fun closeIssue(id: Int)

    suspend fun reopenIssue(id: Int)
}
```

Domain-модель:

```kotlin
data class Issue(
    val id: Int,
    val title: String,
    val state: IssueState,
    val labels: Set<String>,
    val url: String,
    val updatedAt: Instant
)
```

---

# 7. Внутренняя архитектура

Пример модулей:

```text
cli/
├── command/
│   ├── PullCommand
│   ├── PushCommand
│   ├── StatusCommand
│   ├── NewCommand
│   ├── StartCommand
│   ├── DoneCommand
│   └── PrCommand
│
├── kanban/
│   ├── KanbanParser
│   ├── KanbanWriter
│   └── KanbanTask
│
├── tasks/
│   ├── TaskNoteParser
│   └── TaskNoteWriter
│
├── issues/
│   ├── IssueProvider
│   ├── github/
│   ├── gitlab/
│   └── sourcecraft/
│
├── git/
│   └── GitRepository
│
├── config/
│   └── ProjectConfig
│
└── domain/
    ├── Task
    ├── TaskStatus
    └── Issue
```

---

# 8. Kanban parser

CLI должен уметь читать:

```md
## Todo

- [ ] [[Tasks/57 Analytics API]] `#57`
```

И преобразовывать это в:

```kotlin
data class KanbanTask(
    val issueId: Int?,
    val title: String,
    val notePath: String?,
    val status: TaskStatus,
    val completed: Boolean
)
```

Parser должен:

1. найти configured headings;
    
2. определить статус задачи по heading;
    
3. распарсить Markdown checkbox;
    
4. распарсить Obsidian wikilink;
    
5. получить issue ID;
    
6. сохранить исходное положение задачи.
    

Не нужно делать полноценный Markdown parser на первой версии.

Достаточно поддерживать строго определённый формат карточек.

---

# 9. Основные команды

## `yi status`

Показывает расхождения между Obsidian и remote tracker.

```bash
yi status
```

Пример:

```text
Issue   Local          Remote         State
──────────────────────────────────────────────
#42     In Progress    In Progress    ✓
#57     Todo           Backlog        !
#73     Completed      Todo           !
```

Команда ничего не изменяет.

Она должна быть безопасной и использоваться перед `pull` / `push`.

---

## `yi pull`

Remote → Obsidian.

```bash
yi pull
```

Remote tracker является источником истины.

CLI:

1. получает remote issues;
    
2. определяет статус каждого issue;
    
3. находит соответствующую карточку в Kanban;
    
4. перемещает карточку в нужную колонку;
    
5. создаёт отсутствующие локальные задачи;
    
6. обновляет title при необходимости.
    

Например:

```text
Remote:
#57 → In Progress

Local:
#57 → Todo
```

После:

```text
Todo → In Progress
```

---

# 10. `yi push`

Obsidian → Remote.

```bash
yi push
```

Obsidian является источником истины.

CLI:

1. парсит Kanban;
    
2. сравнивает состояние с remote;
    
3. обновляет status labels;
    
4. закрывает issues из Completed;
    
5. reopen issue при переносе из Completed обратно;
    
6. создаёт Issues для локальных задач без issue ID.
    

Перед применением изменений желательно показывать diff:

```text
Changes:

#42 status:
  todo → in-progress

#57:
  close issue

New:
  "Добавить Creative Preview"
  → create issue
```

---

# 11. Создание новой задачи

Пользователь может создать карточку непосредственно в Obsidian:

```md
## Backlog

- [ ] Добавить preview generated creative
```

Затем:

```bash
yi push
```

CLI:

1. обнаруживает карточку без `#ID`;
    
2. создаёт remote Issue;
    
3. получает ID, например `#73`;
    
4. создаёт Task note:
    

```text
Tasks/73 Creative Preview.md
```

5. заменяет карточку:
    

```md
- [ ] [[Tasks/73 Creative Preview]] `#73`
```

Task note:

```md
---
issue: 73
---

# Creative Preview

## Context

## Implementation plan

## Notes
```

---

# 12. `yi new`

Альтернативный способ создания задачи:

```bash
yi new "Добавить preview generated creative"
```

По умолчанию:

1. создаётся remote Issue;
    
2. создаётся Task note;
    
3. задача добавляется в Backlog.
    

Опционально:

```bash
yi new "..." --status todo
```

---

# 13. Начало работы

```bash
yi start 42
```

Команда должна:

1. найти Issue `#42`;
    
2. переместить карточку в `In Progress`;
    
3. обновить remote status;
    
4. определить git repository;
    
5. создать branch.
    

Например:

```text
feat/42-yandex-oauth
```

Если branch уже существует — переключиться на неё.

Пример результата:

```text
✓ #42 moved to In Progress
✓ Remote issue updated
✓ Switched to feat/42-yandex-oauth
```

---

# 14. Git integration

Git integration должна быть отдельной от IssueProvider.

Интерфейс:

```kotlin
interface GitRepository {

    fun currentBranch(): String

    fun branchExists(name: String): Boolean

    fun createBranch(name: String)

    fun checkout(name: String)
}
```

Branch template:

```text
{type}/{issue}-{slug}
```

Пример:

```text
feat/42-yandex-oauth
fix/57-auth-crash
refactor/73-network-layer
```

---

# 15. `yi done`

```bash
yi done 42
```

Минимальная версия:

1. переместить задачу в Completed;
    
2. поставить `[x]`;
    
3. закрыть remote Issue.
    

Но предпочтительный workflow:

```text
start
↓
development
↓
PR
↓
merge
↓
completed
```

Поэтому закрытие Issue желательно привязывать к merge PR, если provider это позволяет.

---

# 16. Pull Request integration

Команда:

```bash
yi pr 42
```

Создаёт PR/MR для текущей ветки.

Title:

```text
Implement Yandex OAuth
```

Body:

```md
Closes #42
```

Дополнительно можно добавить содержимое раздела:

```md
## Implementation plan
```

из Task note.

На первой версии автоматическое создание PR не является обязательным.

---

# 17. Конфликты

Не делать скрытую автоматическую bidirectional synchronization.

Использовать явную семантику:

```text
yi pull
remote wins

yi push
local wins
```

Для проверки:

```text
yi status
```

Позже можно добавить:

```bash
yi sync
```

Но только после появления нормальной модели conflict resolution.

---

# 18. `yi sync`

Не реализовывать в MVP.

В дальнейшем:

```bash
yi sync
```

может сравнивать:

- remote `updatedAt`;
    
- локальную историю изменений;
    
- предыдущий sync state.
    

Для этого понадобится локальный state file:

```text
~/.local/state/yi/
```

или:

```text
.obsidian-issue-sync/
```

Например:

```json
{
  "issues": {
    "42": {
      "lastRemoteUpdatedAt": "...",
      "lastLocalStatus": "todo",
      "lastRemoteStatus": "todo"
    }
  }
}
```

Это позволит определять:

```text
Local changed only  → push
Remote changed only → pull
Both changed        → conflict
```

---

# 19. Auth

Не хранить access tokens в Obsidian.

Предпочтительный порядок:

### GitHub

Использовать существующую авторизацию `gh`:

```bash
gh auth status
```

CLI может либо:

- вызывать `gh`;
    
- либо получать token через `gh auth token`.
    

### GitLab

Использовать `glab`.

### SourceCraft

Использовать официальный CLI/API, если доступен.

Fallback:

- environment variables;
    
- system keyring.
    

Например:

```text
GITHUB_TOKEN
GITLAB_TOKEN
SOURCECRAFT_TOKEN
```

---

# 20. Provider implementation

На первой версии не пытаться поддержать все три сервиса одновременно.

Порядок:

1. GitHub или GitLab — в зависимости от текущего проекта.
    
2. стабилизировать `IssueProvider`;
    
3. добавить второй provider;
    
4. добавить SourceCraft.
    

Это позволит проверить, действительно ли abstraction достаточно универсальна.

---

# 21. Работа с Codex

Task note может выступать контекстом coding agent.

Команда:

```bash
yi agent 42
```

может найти:

```text
Tasks/42 Yandex OAuth.md
```

и передать содержимое агенту.

Например концептуально:

```bash
codex "$(cat 'Tasks/42 Yandex OAuth.md')"
```

Но лучше сформировать prompt:

```text
Work on issue #42.

Read the following task context:

<task note>

Repository:
~/Projects/yandex-ads

Current branch:
feat/42-yandex-oauth
```

---

# 22. Возможный end-to-end workflow

Создание задачи:

```text
Obsidian:

Backlog
- [ ] Сделать Yandex OAuth
```

↓

```bash
yi push
```

↓

```text
GitHub Issue #42

Tasks/42 Yandex OAuth.md

Kanban:
- [ ] [[Tasks/42 Yandex OAuth]] `#42`
```

Начало работы:

```bash
yi start 42
```

↓

```text
Backlog/Todo → In Progress

GitHub:
status:in-progress

Git:
feat/42-yandex-oauth
```

Работа:

```text
Tasks/42 Yandex OAuth.md

## Implementation plan

- [x] Deep link
- [x] OAuth callback
- [ ] Exchange
```

↓

```bash
yi pr 42
```

↓

```text
Pull Request

Closes #42
```

↓

PR merged

↓

```bash
yi pull
```

↓

```text
#42 → Completed
```

---

# 23. MVP

Первая версия должна решать только основную проблему.

## Обязательно

-  чтение `.issue-sync.yml`;
    
-  чтение `Kanban.md`;
    
-  парсинг четырёх колонок;
    
-  парсинг issue ID;
    
-  `IssueProvider`;
    
-  один provider;
    
-  получение Issues;
    
-  создание Issue;
    
-  update status labels;
    
-  close/reopen Issue;
    
-  `yi status`;
    
-  `yi pull`;
    
-  `yi push`;
    
-  создание Task note;
    
-  обновление `Kanban.md`.
    

## После MVP

-  `yi new`;
    
-  `yi start`;
    
-  создание branch;
    
-  `yi done`;
    
-  `yi pr`;
    
-  Codex integration;
    
-  несколько providers;
    
-  conflict detection;
    
-  `yi sync`.
    

---

# 24. Этапы реализации

## Этап 1 — Domain + Kanban parser

Реализовать:

```text
Task
TaskStatus
KanbanBoard
KanbanParser
KanbanWriter
ProjectConfig
```

Тесты:

- чтение всех колонок;
    
- чтение wikilink;
    
- чтение issue ID;
    
- карточка без Issue;
    
- Completed checkbox;
    
- перемещение задачи между колонками;
    
- сохранение остальных частей Markdown.
    

Главное требование: writer не должен уничтожать пользовательский Markdown.

---

## Этап 2 — IssueProvider

Реализовать abstraction:

```text
IssueProvider
```

и первый provider.

Операции:

```text
list
get
create
update labels
close
reopen
```

---

## Этап 3 — Status

Реализовать:

```bash
yi status
```

На этом этапе CLI уже должен уметь сопоставлять:

```text
Kanban Task ↔ Remote Issue
```

Но ничего не изменять.

---

## Этап 4 — Pull

Реализовать:

```bash
yi pull
```

Remote → local.

Особое внимание:

- неизвестные Issues;
    
- закрытые Issues;
    
- переименованные Issues;
    
- отсутствующие Task notes.
    

---

## Этап 5 — Push

Реализовать:

```bash
yi push
```

Local → remote.

Особое внимание:

- создание нового Issue;
    
- изменение status;
    
- close/reopen;
    
- вывод diff перед изменениями.
    

---

## Этап 6 — Git workflow

Добавить:

```bash
yi start <issue>
```

и:

```text
GitRepository
BranchNameGenerator
```

---

## Этап 7 — PR workflow

Добавить:

```bash
yi pr <issue>
```

и связывание:

```text
Task
↔
Issue
↔
Branch
↔
PR
```

---

## Этап 8 — Coding agents

Добавить:

```bash
yi agent <issue>
```

для запуска Codex с контекстом Task note.

---

# 25. Принципы

CLI должен придерживаться нескольких правил.

### Obsidian остаётся Obsidian

`Kanban.md` не превращается в сериализованную базу данных.

### Task note и Issue имеют разную роль

Issue:

- коммуникация команды;
    
- acceptance criteria;
    
- общий lifecycle.
    

Task note:

- implementation details;
    
- research;
    
- заметки;
    
- context для agent.
    

### Явная синхронизация лучше магической

На первой версии:

```text
status
pull
push
```

вместо фоновой автоматической синхронизации.

### Issue ID — главный идентификатор

Не создавать отдельную систему ID без необходимости.

### Provider-specific code изолирован

Core CLI ничего не должен знать про особенности GitHub/GitLab/SourceCraft.

---

# 26. Итоговая минимальная UX

Основные команды:

```bash
yi status
yi pull
yi push

yi new "Task title"
yi start 42
yi done 42
yi pr 42
```

Типичный рабочий день:

```bash
yi pull

yi status

yi start 42

# development

yi pr 42

# после merge

yi pull
```

При этом Obsidian остаётся главным локальным интерфейсом:

```text
Kanban.md
    ↓
Tasks/*.md
    ↓
Issue
    ↓
Branch
    ↓
PR
```