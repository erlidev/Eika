-- Remote credentials are environment references. Secret values must not be
-- stored with a project. Older rows can contain secrets in URL userinfo,
-- query strings, or fragments. Strip those parts and give each affected
-- remote the generic environment references documented for operators.
ALTER TABLE projects
    ADD COLUMN remote_username_env text NOT NULL DEFAULT '',
    ADD COLUMN remote_password_env text NOT NULL DEFAULT '';

UPDATE projects
SET remote_url = regexp_replace(
        regexp_replace(
            remote_url,
            '^([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@',
            '\1'
        ),
        '[?#].*$',
        ''
    ),
    remote_username_env = 'EIKA_GIT_USERNAME',
    remote_password_env = 'EIKA_GIT_PASSWORD'
WHERE kind = 'remote'
  AND (
      remote_url ~ '^[A-Za-z][A-Za-z0-9+.-]*://[^/?#]*@'
      OR remote_url ~ '[?#]'
  );
