reset-db:
	docker exec fixed-app-1 psql -c 'drop schema public cascade'

deploy:
	docker compose -p myapp up -d
