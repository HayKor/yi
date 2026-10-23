# yi: статус из GitHub Projects (v2)

Дополнение к `2026-10-21-yi-kanban-sync-design.md`. Решения согласованы 2026-10-23.

## Цель

Одна доска вместо двух. Если в конфиге указан GitHub Project, колонка карточки синхронизируется
с полем **Status** этого проекта. Labels `status:*` при этом не читаются и не меняются. Без
проекта всё работает как раньше: статус хранится в labels.

## Конфиг

```yaml
provider: github
repository: haykor/CarManager
github:
  project: 1              # номер проекта; владелец — владелец repository
  # project: someorg/5    # проект другого владельца (user или organization)
statuses:
  todo:
    heading: Todo
    # projectStatus: To do  # опция поля Status; по умолчанию = heading
    # label: status:todo    # нужен только без project
```

- `statuses.<key>.projectStatus` — имя опции поля Status. По умолчанию это `heading`.
  Сравнение без учёта регистра и пробелов по краям.
- `label` в конфиге необязателен. Обязательность проверяет провайдер, которому нужны labels
  (GitHub без `project`): `provider.RequireLabels`. Дубликаты labels ловит config, как раньше.
- При старте провайдер один раз читает проект: id, поле `Status`, опции. Ошибка возникает,
  если проекта нет, если в нём нет single-select поля `Status` или если у какой-то колонки
  нет опции. В последнем случае сообщение перечисляет доступные опции и предлагает
  `projectStatus`. Лишние опции в проекте разрешены.

## Модель

- `domain.Status.ProjectStatus` (resolved: `projectStatus` или heading).
- `domain.Issue.HasStatusField bool` + `Status string`: провайдер хранит статус в поле
  трекера; `Status` — значение поля, `""` если issue нет в проекте или поле пустое.
- `provider.IssueUpdate.Status *string`: имя опции; провайдер при необходимости сам
  добавляет issue в проект.
- `provider.Config.Statuses`: провайдер получает колонки (для проверки опций/labels).
- Опциональный интерфейс `provider.StatusFielder{ StatusField() bool }` +
  `provider.UsesStatusField(p)`: нужно только при создании issue, чтобы не вешать label в
  режиме проекта. Всё остальное решается по `Issue.HasStatusField`.

## Правила

`Config.StatusOf(issue)`:
1. closed → close-статус (как раньше).
2. `HasStatusField`:
   - `Status == ""` → `defaultStatus`.
   - совпала опция → её статус.
   - не совпала ни одна колонка (например, «Review») → `""` + warning
     `#5 has project status "Review", which no column maps to`.
3. иначе — labels (как раньше).

Пустой ключ (`""`) значит, что статус неизвестен. Pull в этом случае не двигает существующую
карточку, а новую кладёт в `defaultStatus`. Status показывает `-` в колонке Remote.

`RemoteUpdateFor` в режиме поля ставит `Status = projectStatus`, если значение отличается
(в том числе когда issue нет в проекте, и тогда её добавят). Плюс close/reopen как раньше.
Labels не трогаются.

Создание issue (`push`, `yi new`): в режиме поля без labels; затем
`RemoteUpdateFor(created, column)` выставляет Status (и закрывает для closeIssue-колонки) — уже
после записи id на карточку, как сейчас закрытие. Issue вне проекта при pull попадает в
`defaultStatus`, а следующий push добавляет её в проект.

## GitHub

GraphQL через тот же go-github клиент (`NewRequest` + `Do`: та же авторизация, rate limit,
ошибки). Endpoint: `<base>/graphql`, для Enterprise `/api/v3/` → `/api/graphql`. Ошибки из
`errors` GraphQL превращаются в ошибку `github: <op>: <messages>`.

- `yiProject` — `repositoryOwner(login){ ... on ProjectV2Owner { projectV2(number) { id
  field(name:"Status") { ... on ProjectV2SingleSelectField { id options { id name } } } } } }`.
- `yiProjectItems` — все items проекта (по 100), из них issues этого repository: номер → item
  id, значение Status. Загружается лениво, один раз за запуск.
- `yiAddItem` — `addProjectV2ItemById` (идемпотентна: для уже добавленной issue вернёт её item).
- `yiSetStatus` — `updateProjectV2ItemFieldValue` с `singleSelectOptionId`.

`UpdateIssue`: сначала REST (title/state/labels), потом Status: node id issue → `yiAddItem` →
`yiSetStatus`. Status ставится последним, чтобы перебить project workflow «closed → Done».

Нужен scope `project` у токена (`gh auth refresh -s project`); без него GraphQL вернёт ошибку,
и её текст будет в сообщении `yi`.

## Тесты

- config: `projectStatus` default/override; `label` необязателен; `StatusOf` в режиме поля.
- reconcile: `RemoteUpdateFor` (поле), pull с неизвестной опцией не двигает карточку, push
  создаёт issue без labels и ставит Status.
- fake: `fake.NewWithStatusField()`; providertest: `RunStatusField` — общий контракт для fake и
  GitHub (fake GraphQL в `fakeserver_test.go`).
- github: проверки при старте (нет проекта, нет поля, нет опции), Enterprise URL GraphQL.

## Порядок работы

1. domain/provider/config: типы, `projectStatus`, необязательный label, `StatusOf`.
2. reconcile + cli: `RemoteUpdateFor`, неизвестный статус, создание issue.
3. fake + `providertest.RunStatusField`.
4. GitHub: GraphQL-клиент, проект, items, add/set; fake GraphQL server; тесты.
5. README, `yi init` (закомментированный `project`), проверка на CarManager (dry-run).
