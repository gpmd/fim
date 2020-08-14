# File Intergrity Monitor

This project was necessary as we are using Capistrano for deployment, meaning the document root we are trying to check is only a 
symlink, that changes every time we deploy.

We also want to be notified on slack, have a log file.

I used https://github.com/jpsthecelt/fimTree as a starting point. Although the current code is quite suboptimal (especially handling concurrency) 
it does the job without any external requirements.

# Running it from cron

	*/5 * * * *  /home/www-data/fim /home/www-data/config.json

# Config file format

```json
{
    "logfile": "/home/www-data/changes.log",
    "storage": "/home/www-data/checksums.json",
    "folders": ["/home/www-data/htdocs/live/http", "/var/www/site/___shared/app", "/var/www/site/___shared/blogs", "/var/www/site/___shared/wp"],
    "ignored": [],
    "slack_chat_id": "{CHANNEL_IDENTIFIER_HASH}",
    "slack_token": "{TOKEN_FROM_SLACK}"
}
```
Checksum storage (that's storing the last state) and log files are the only files the application will modify.

Folders are checked, ignored list is concatenated to folders (all of them) and ignored for change handling, as well as internal symlinks,
add symlinked folders that needs checking (the ___shared ones in the example config).
