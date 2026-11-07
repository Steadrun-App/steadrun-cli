# steadrun

[Русский](#русский) | [English](#english)

---

## Русский

Обёртка для cron-задач и скриптов: сообщает [Steadrun](https://steadrun.ru), что команда запустилась,
отработала или упала. Если упала, в алерт попадают код выхода и хвост вывода.

```bash
steadrun run ВАШ-UUID -- pg_dump -Fc mydb -f /backup/db.dump
```

Подробная документация: [steadrun.ru/docs/cli](https://steadrun.ru/docs/cli).

### Установка

Один статический бинарник без зависимостей. Linux x86-64:

```bash
sudo curl -fsSL -o /usr/local/bin/steadrun https://github.com/Steadrun-App/steadrun-cli/releases/latest/download/steadrun-linux-amd64
sudo chmod +x /usr/local/bin/steadrun
```

Для Linux ARM замените в адресе `steadrun-linux-amd64` на `steadrun-linux-arm64`, для macOS на Apple Silicon — на
`steadrun-darwin-arm64`. Контрольные суммы лежат в `checksums.txt` рядом с бинарниками.

Если установлен Go: `go install github.com/Steadrun-App/steadrun-cli/cmd/steadrun@latest`.

### Как работает

1. Перед запуском команды — `GET /ping/{uuid}/start`.
2. Команда работает как обычно: вывод идёт в терминал или в письмо cron без изменений, stdin передаётся ей.
3. Код выхода 0 — `GET /ping/{uuid}`. Любой другой — `POST /ping/{uuid}/fail`, в теле строка `Exit code: N` и
   последние 8 КБ вывода (stdout и stderr вместе).
4. `steadrun` завершается с кодом выхода команды, поэтому `&&` в crontab работает как раньше.

SIGINT и SIGTERM передаются команде. Если её убили сигналом, это падение с кодом `128 + номер сигнала`, как в shell.
Если команда не нашлась, код выхода 127.

Каждый запрос к Steadrun ждёт не дольше 5 секунд. Если Steadrun недоступен или UUID неверный, `steadrun`
напишет одну строку в stderr, а команда запустится и завершится как обычно.

### Пример для crontab

```
0 3 * * * /usr/local/bin/steadrun run ВАШ-UUID -- /home/deploy/backup.sh
```

### Переменные окружения

- `STEADRUN_BASE_URL` — адрес Steadrun, по умолчанию `https://steadrun.ru`.

### Что уходит в Steadrun

Хвост вывода упавшей команды сохраняется в Steadrun и приходит в алертах на почту, в Telegram и на вебхук. Не
оборачивайте команды, которые печатают пароли, токены или персональные данные.

### Версии

До 1.0 аргументы командной строки, переменные окружения и формат тела `/fail` ещё могут измениться. История — в
[GitHub Releases](https://github.com/Steadrun-App/steadrun-cli/releases).

### Лицензия

MIT.

---

## English

A wrapper for cron jobs and scripts: tells [Steadrun](https://steadrun.ru) that a command started, finished or
failed. On failure, the alert includes the exit code and the tail of the output.

```bash
steadrun run YOUR-UUID -- pg_dump -Fc mydb -f /backup/db.dump
```

Full documentation (in Russian): [steadrun.ru/docs/cli](https://steadrun.ru/docs/cli).

### Install

A single static binary with no dependencies. Linux x86-64:

```bash
sudo curl -fsSL -o /usr/local/bin/steadrun https://github.com/Steadrun-App/steadrun-cli/releases/latest/download/steadrun-linux-amd64
sudo chmod +x /usr/local/bin/steadrun
```

For Linux ARM use `steadrun-linux-arm64` in the URL, for macOS on Apple Silicon `steadrun-darwin-arm64`. Checksums
are in `checksums.txt` next to the binaries.

With Go installed: `go install github.com/Steadrun-App/steadrun-cli/cmd/steadrun@latest`.

### How it works

1. Before the command starts: `GET /ping/{uuid}/start`.
2. The command runs as usual: output goes to the terminal or the cron email unchanged, and stdin is passed through.
3. Exit code 0: `GET /ping/{uuid}`. Anything else: `POST /ping/{uuid}/fail` with `Exit code: N` and the last 8 KB
   of output (stdout and stderr combined).
4. `steadrun` exits with the command's exit code, so `&&` in crontab keeps working.

SIGINT and SIGTERM are forwarded to the command. If a signal kills it, that's a failure with code
`128 + signal number`, as in a shell. If the command isn't found, the exit code is 127.

Each request to Steadrun waits at most 5 seconds. If Steadrun is unreachable or the UUID is wrong, `steadrun`
prints one line to stderr, and the command runs and exits as usual.

### Crontab example

```
0 3 * * * /usr/local/bin/steadrun run YOUR-UUID -- /home/deploy/backup.sh
```

### Environment

- `STEADRUN_BASE_URL` — Steadrun URL, defaults to `https://steadrun.ru`.

### What is sent to Steadrun

The output tail of a failed command is stored in Steadrun and included in email, Telegram and webhook alerts. Don't
wrap commands that print passwords, tokens or personal data.

### Versioning

Before 1.0, command-line arguments, environment variables and the `/fail` body format may still change. History:
[GitHub Releases](https://github.com/Steadrun-App/steadrun-cli/releases).

### License

MIT.