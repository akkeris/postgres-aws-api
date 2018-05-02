do $$
begin

	create table if not exists provision
	(
		name varchar(200) not null constraint name_pkey primary key,
		plan varchar(200),
		claimed varchar(200),
		make_date timestamp default now(),
		masterpass varchar(200),
		masteruser varchar(200),
		endpoint varchar(200)
	);

	create table if not exists extra_roles
	(
		database varchar(200) not null references provision("name"),
		username varchar(200) not null,
		passwd varchar(200) not null,
		read_only boolean not null,
		make_date timestamp default now(),
		update_date timestamp default now(),
		primary key(database, username)
	);

	create table if not exists shared_tenant
	(
		host varchar(200) not null,
		masterpass varchar(200),
		masteruser varchar(200),
		primary key (host)
	);
end
$$;