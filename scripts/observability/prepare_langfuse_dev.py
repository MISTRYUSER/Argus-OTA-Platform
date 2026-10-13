"""Prepare fresh, private credentials for the disposable local validation overlay.

Never overwrites credentials or reads provider/user secrets. Bootstrap the separate
PostgreSQL role/database by piping generated SQL to psql after PostgreSQL is ready.
"""
import argparse
import base64
import os
import pathlib
import secrets


def prepare(directory):
    directory.mkdir(parents=True, exist_ok=True)
    env_path = directory / 'langfuse-private.env'
    init_path = directory / 'langfuse-postgres-init.sql'
    if env_path.exists() or init_path.exists():
        raise ValueError('private files already exist; choose a fresh directory or reuse them')
    secret = lambda: secrets.token_hex(24)
    values = {key: secret() for key in [
        'LANGFUSE_CLICKHOUSE_PASSWORD', 'LANGFUSE_SALT', 'LANGFUSE_NEXTAUTH_SECRET',
        'LANGFUSE_INIT_USER_PASSWORD', 'LANGFUSE_DEV_POSTGRES_PASSWORD',
        'LANGFUSE_DEV_REDIS_PASSWORD', 'LANGFUSE_DEV_MINIO_ROOT_PASSWORD',
        'LANGFUSE_S3_SECRET_ACCESS_KEY']}
    values['LANGFUSE_ENCRYPTION_KEY'] = secrets.token_hex(32)
    values['LANGFUSE_PUBLIC_KEY'] = 'pk-lf-' + secrets.token_hex(16)
    values['LANGFUSE_SECRET_KEY'] = 'sk-lf-' + secrets.token_hex(24)
    values['LANGFUSE_AUTH_BASE64'] = base64.b64encode(
        (values['LANGFUSE_PUBLIC_KEY'] + ':' + values['LANGFUSE_SECRET_KEY']).encode()).decode()
    values['LANGFUSE_INIT_USER_EMAIL'] = 'argus-local@example.invalid'
    values['LANGFUSE_DEV_MINIO_ROOT_USER'] = 'argusdev' + secrets.token_hex(4)
    values['LANGFUSE_S3_ACCESS_KEY_ID'] = 'langfuse' + secrets.token_hex(4)
    db_password = secret()
    values['LANGFUSE_DATABASE_URL'] = 'postgresql://langfuse:' + db_password + '@langfuse-dev-postgres:5432/langfuse'
    values['LANGFUSE_REDIS_URL'] = 'redis://:' + values['LANGFUSE_DEV_REDIS_PASSWORD'] + '@langfuse-dev-redis:6379/0'
    values['LANGFUSE_S3_ENDPOINT'] = 'http://langfuse-dev-minio:9000'
    values['LANGFUSE_S3_PUBLIC_ENDPOINT'] = 'http://127.0.0.1:19000'
    values['LANGFUSE_BASE_URL'] = 'http://127.0.0.1:3000'
    fd = os.open(init_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as file:
        file.write("CREATE USER langfuse PASSWORD '" + db_password + "';\nCREATE DATABASE langfuse OWNER langfuse;\n")
    fd = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as file:
        file.write('\n'.join(key + '=' + value for key, value in values.items()) + '\n')
    return env_path, init_path


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=pathlib.Path, required=True)
    paths = prepare(parser.parse_args().directory)
    print('Generated private local credentials and bootstrap SQL, mode 600:', ', '.join(map(str, paths)))
