# LoyaltyLedger

HTTP-сервис программы лояльности интернет-магазина на Go и PostgreSQL. Пользователи загружают номера своих заказов, получают и списывают бонусные баллы. Администратор просматривает пользователей и заказы, управляет блокировками и экспортирует статистику. Веб-интерфейс отсутствует.

## Запуск через Docker Compose

Требуются Docker и Docker Compose v2. Команды выполняются из корня проекта.

```bash
cp .env.example .env
docker compose --profile local up --build -d
```

Будут запущены приложение, PostgreSQL и локальная заглушка Order Service с данными из `testdata/orders.json`.

```bash
curl -i http://localhost:8080/readyz
docker compose ps
docker compose logs -f app
```

Приложение доступно на `http://localhost:8080`, заглушка — на `http://localhost:8081`. Ответ `200` от `/readyz` означает, что приложение подключено к PostgreSQL.

При первом запуске автоматически применяются миграции и создаётся администратор из `ADMIN_LOGIN` и `ADMIN_PASSWORD`. Пример `.env` содержит логин `admin` и пароль `change-this-admin-password`. Задайте собственные значения перед использованием вне локальной среды. Пароль существующего администратора при перезапуске не изменяется.

```bash
docker compose restart app
docker compose --profile local down
```

Данные сохраняются в томе `postgres_data`. Обычный `down` не удаляет его.

## Конфигурация

Настройки читаются из `config.yaml` и переопределяются переменными среды. Другой путь к YAML задаётся через `CONFIG_FILE`.

| Переменная | Назначение |
| --- | --- |
| `POSTGRES_PASSWORD` | Пароль PostgreSQL в Compose; используйте буквы, цифры и дефисы, поскольку пароль подставляется в URL подключения |
| `DATABASE_URL` | URL подключения при запуске без Compose; в Compose формируется автоматически |
| `ADMIN_LOGIN`, `ADMIN_PASSWORD` | Учётные данные для создания администратора |
| `ORDER_SERVICE_URL` | Адрес внешнего сервиса; для локального профиля — `http://orderstub:8081` |
| `ORDER_SERVICE_API_KEY` | Необязательный ключ внешнего сервиса, передаётся как Bearer-токен |
| `SERVER_HOST`, `SERVER_PORT` | Адрес HTTP-сервера; по умолчанию `127.0.0.1:8080`, в контейнере — `0.0.0.0:8080` |
| `ACCRUAL_INTERVAL_SECONDS` | Интервал обработки заказов, по умолчанию 3 секунды |
| `WORKER_CONCURRENCY` | Число параллельно обрабатываемых заказов, по умолчанию 5 |
| `ORDER_SERVICE_TIMEOUT_SECONDS` | Таймаут внешнего запроса, по умолчанию 5 секунд |
| `TOKEN_TTL_HOURS` | Срок действия токена, по умолчанию 24 часа |
| `REWARD_PERCENT` | Процент начисления, по умолчанию 5 |
| `LOG_LEVEL` | `info` или `debug` |

Переменные из `.env` читает Docker Compose. При запуске через `go run` их нужно экспортировать в оболочку. Настройки, не перечисленные в `environment` сервиса `app` в Compose, можно изменить в `config.yaml` и пересобрать образ.

## Подключение Order Service

Для подключения внешнего сервиса укажите его адрес и ключ в `.env`:

```dotenv
ORDER_SERVICE_URL=https://orders.example.com
ORDER_SERVICE_API_KEY=your-service-key
```

Запустите приложение и базу без локального профиля:

```bash
docker compose up --build -d postgres app
```

Если заглушка была запущена ранее, остановите её:

```bash
docker compose --profile local stop orderstub
```

Сервис должен отвечать на `GET /api/orders/{number}`:

```json
{
  "number": "79927398713",
  "owner_id": 2,
  "status": "COMPLETED",
  "amount": 1000.00
}
```

`owner_id` — ID пользователя LoyaltyLedger, связанный с клиентом магазина на стороне интеграции. `amount` — сумма заказа в рублях. Допустимые состояния: `CREATED`, `PAID`, `COMPLETED`, `CANCELLED`. Отсутствующий заказ должен возвращать `404`.

За `COMPLETED` начисляется установленный процент суммы с округлением вниз до 0.01 балла. `CREATED` и `PAID` ожидают завершения. Отмена заказа до начисления завершает его обработку без бонусов. Ставка фиксируется при загрузке заказа. При временном сбое внешнего сервиса фоновая обработка повторяется.

Списание разрешено на собственный заказ в состоянии `CREATED`, в пределах его суммы и доступного баланса. Применяется курс 1 балл = 1 рубль. LoyaltyLedger фиксирует списание в бонусном реестре; применение скидки при оформлении покупки выполняет интернет-магазин.

## Использование API

Регистрация:

```bash
curl -i -X POST http://localhost:8080/api/user/register \
  -H 'Content-Type: application/json' \
  -d '{"login":"alice","password":"alice-password"}'
```

Для входа отправьте те же поля на `POST /api/user/login`. Логин должен содержать 3–64 символа без пробелов, пароль — 8–72 байта. Токен возвращается в поле `token` и заголовке `Authorization`.

```bash
TOKEN='полученный_токен'
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/user/me
curl -i -X POST http://localhost:8080/api/user/orders \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: text/plain' \
  --data '79927398713'
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/user/balance
```

На пустой БД первый обычный пользователь после администратора получает ID `2`. Локальные заказы `79927398713` (`COMPLETED`, 1000.00 руб.) и `12345678903` (`CREATED`, 500.00 руб.) принадлежат пользователю `2`. Если ID другой, измените `owner_id` в `testdata/orders.json` и пересоберите заглушку:

```bash
docker compose --profile local up --build -d orderstub
```

После обработки заказа `79927398713` при ставке 5% начисляется 50.00 баллов. Списание:

```bash
curl -i -X POST http://localhost:8080/api/user/balance/withdraw \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"order":"12345678903","sum":20.00}'
```

Для одного заказа допускается одно списание. Повтор той же суммы возвращает успех без повторного уменьшения баланса; другая сумма — `409`.

| Метод | Маршрут | Доступ | Действие |
| --- | --- | --- | --- |
| POST | `/api/user/register` | Без токена | Регистрация |
| POST | `/api/user/login` | Без токена | Вход |
| GET | `/api/user/me` | Пользователь, администратор | Свой профиль |
| POST / GET | `/api/user/orders` | Пользователь | Загрузка / список своих заказов |
| GET | `/api/user/balance` | Пользователь | Баланс |
| POST | `/api/user/balance/withdraw` | Пользователь | Списание |
| GET | `/api/user/withdrawals` | Пользователь | История списаний |
| GET | `/api/admin/users` | Администратор | Список пользователей |
| GET | `/api/admin/orders` | Администратор | Список заказов |
| PATCH | `/api/admin/users/{id}/block` | Администратор | Блокировка / разблокировка |
| GET | `/api/admin/stats` | Администратор | Статистика |
| GET | `/api/admin/stats/export` | Администратор | Скачать CSV |
| POST | `/api/stats/export` | Администратор | Скачать CSV |

Списки принимают `limit` от 1 до 100 и `offset` от 0. По умолчанию — `limit=50&offset=0`. Для пустых пользовательских списков возвращается `204`.

Для блокировки авторизуйтесь администратором через `/api/user/login`, затем:

```bash
ADMIN_TOKEN='токен_администратора'
curl -i -X PATCH http://localhost:8080/api/admin/users/2/block \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"blocked":true}'
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/api/admin/stats/export -o loyaltyledger-stats.csv
```

Для разблокировки передайте `{"blocked":false}`. После разблокировки пользователь должен войти заново. Администратор не может выполнять списания своим токеном или изменять историю операций.

## Запуск без Docker

Требуются Go 1.22+ и PostgreSQL, отдельно доступный с хоста по адресу `localhost:5432`. Сервис PostgreSQL из текущего `compose.yaml` не публикует порт на Mac, поэтому приведённый ниже адрес к базе из Compose не подключится. Для контейнерного запуска используйте инструкции выше.

```bash
export DATABASE_URL='postgres://loyaltyledger:local-change-me-2026@localhost:5432/loyaltyledger?sslmode=disable'
export ORDER_SERVICE_URL='http://localhost:8081'
export ORDER_SERVICE_API_KEY='local-order-service-key'
export ADMIN_LOGIN='admin'
export ADMIN_PASSWORD='change-this-admin-password'
go mod download
go run ./cmd/server
```

Заглушка запускается в другом терминале:

```bash
ORDER_SERVICE_API_KEY=local-order-service-key go run ./cmd/orderstub
```

## Тесты

Через Docker, после создания `.env`:

```bash
docker compose --profile test run --rm tests
```

С установленным Go:

```bash
go mod download
go test -race ./...
go vet ./...
go build ./...
```

Для интеграционных тестов задайте `TEST_DATABASE_URL` с адресом PostgreSQL. Тесты создают временные схемы; пользователь БД должен иметь право `CREATE SCHEMA`. Без этой переменной интеграционные тесты пропускаются.
