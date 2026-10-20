package sqlitestore

const forumRootsSQL = `
create table channels_forum (
  id integer primary key,
  account_id integer not null references accounts(id),
  tg_channel_id text not null,
  topic_id integer not null default 0 check(topic_id >= 0),
  access_hash text,
  title text not null,
  username text,
  invite_link text,
  root_local_path text not null,
  root_remote_path text not null default '/',
  strategy text not null default 'single',
  discussion_tg_channel_id text not null default '',
  discussion_access_hash text not null default '',
  discussion_title text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(account_id, tg_channel_id, topic_id)
);
insert into channels_forum (id, account_id, tg_channel_id, access_hash, title, username, invite_link, root_local_path, root_remote_path, strategy, discussion_tg_channel_id, discussion_access_hash, discussion_title, created_at, updated_at) select id, account_id, tg_channel_id, access_hash, title, username, invite_link, root_local_path, root_remote_path, strategy, discussion_tg_channel_id, discussion_access_hash, discussion_title, created_at, updated_at from channels;
drop table channels;
alter table channels_forum rename to channels;
`
