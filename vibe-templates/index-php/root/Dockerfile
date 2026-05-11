FROM nginx:latest

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
  && apt-get install -y --no-install-recommends php8.4-fpm php8.4-sqlite3 sqlite3 \
  && rm -rf /var/lib/apt/lists/* \
  && php_fpm="$(find /usr/sbin -maxdepth 1 -name 'php-fpm*' | sort -V | tail -1)" \
  && php_version="${php_fpm#/usr/sbin/php-fpm}" \
  && sed -i 's|^listen = .*|listen = 127.0.0.1:9000|' "/etc/php/${php_version}/fpm/pool.d/www.conf" \
  && printf '\nenv[APP_ENV] = $APP_ENV\nenv[DB_PATH] = $DB_PATH\n' >> "/etc/php/${php_version}/fpm/pool.d/www.conf" \
  && mkdir -p /app/data /var/www/html \
  && chown -R www-data:www-data /app/data /var/www/html

RUN cat > /etc/nginx/conf.d/default.conf <<'NGINX'
server {
    listen 80;
    server_name _;
    root /var/www/html;
    index index.php;

    location / {
        try_files $uri /index.php$is_args$args;
    }

    location ~ \.php$ {
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $document_root;
        fastcgi_pass 127.0.0.1:9000;
    }
}
NGINX

RUN cat > /usr/local/bin/start-index-php <<'SH'
#!/usr/bin/env sh
set -eu
mkdir -p /app/data
chown -R www-data:www-data /app/data
php_fpm="$(find /usr/sbin -maxdepth 1 -name 'php-fpm*' | sort -V | tail -1)"
"$php_fpm" -D
exec nginx -g 'daemon off;'
SH

RUN chmod +x /usr/local/bin/start-index-php

WORKDIR /var/www/html
COPY public/ /var/www/html/

ENV DB_PATH=/app/data/app.sqlite
EXPOSE 80
CMD ["start-index-php"]
