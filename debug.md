# Debugging tips

## Commands
- Execute the program
  - <a name="env"></a>Set the following environment variables
    - GOOGLE_APPLICATION_CREDENTIALS pointing to the credentials json file
    - OAUTH_CLIENT_ID
    - OAUTH_CLIENT_SECRET
    - REFRESH_TOKEN
  - Start postgres using one of the below options.
    - Local fully featured postgres
    - docker container
    ```
    docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres
    ```
  - Update [database.go](db/database.go) file to appropriate value (localhost for local container or postgres if addressable or update /etc/hosts to correct IP)
  - Run the following
  ```
  go run . -oauth_client_id=$OAUTH_CLIENT_ID -oauth_client_secret=$OAUTH_CLIENT_SECRET -refresh_token=$REFRESH_TOKEN
  ```
  - <a name="dbaccess"></a>To access the postgres DB running in docker container
    - `docker exec -it postgres /bin/bash`
    - `psql --u postgres`
- Run individual containers
  - Set the environment variables same shown in [above](#env) step.
  - Create a network (to address postgres by name)
  ```
  docker network create hdd-net
  ```
  - Start postgres attached to the above network
  ```
  docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 --net hdd-net postgres
  ```
  - Build the application container. (Execute at the *base* and not inside the build directory)
  ```
  docker build . -f ./build/Dockerfile -t jyothri/hdd-go-build
  ```
  - Start the application container in the same network
  ```
  docker run \
  -it --rm \
  -e GOOGLE_APPLICATION_CREDENTIALS=/keys/gae_creds.json \
  -e OAUTH_CLIENT_ID=$OAUTH_CLIENT_ID \
  -e OAUTH_CLIENT_SECRET=$OAUTH_CLIENT_SECRET \
  -e REFRESH_TOKEN=$REFRESH_TOKEN \
  -v ~/keys/gae_creds.json:/keys/gae_creds.json:ro \
  -v ~/test:/scan \
  -p 8090:8090 \
  --net hdd-net \
  --name jyothri-hdd \
  jyothri/hdd-go-build
  ```
  - To access the postgres DB. Follow [access](#dbaccess) section.

## <a name="creds"></a>Credentials setup for parsing
- To be able to query Google drive, credentials are provided in the form of OAuth2 token. see [this](https://stackoverflow.com/a/35611334/6487201) answer for instructions. The steps include
  - Setup an oauth client & configure OAuth consent screen. May need to add specific gmail accounts for testing.
  - Obtain authorization code. E.g. https://accounts.google.com/o/oauth2/v2/auth?response_type=code&scope=SCOPE&client_id=CLIENT_ID&state=YOUR_CUSTOM_STATE&redirect_uri=https://local.jkurapati.com&access_type=offline&prompt=consent (e.g SCOPE=https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/gmail.readonly)
  - Exchange AuthZ code for Access & Refresh token. The refresh token can be used by the code.
  ```
  curl --location --request POST 'https://oauth2.googleapis.com/token' \
  --header 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'code=$AUTHZ_CODE' \
  --data-urlencode 'client_id=$CLIENT_ID' \
  --data-urlencode 'client_secret=$CLIENT_SECRET' \
  --data-urlencode 'redirect_uri=https://local.jkurapati.com' \
  --data-urlencode 'grant_type=authorization_code'
  ```
  - [Optional] Use refresh token to get access token.
  ```
  curl --location --request POST 'https://oauth2.googleapis.com/token' \
  --header 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'client_id=$CLIENT_ID' \
  --data-urlencode 'client_secret=$CLIENT_SECRET' \
  --data-urlencode 'grant_type=refresh_token' \
  --data-urlencode 'refresh_token=$REFRESH_TOKEN'
  ```
  - [Optional] Call the Google Drive API
  ```
  curl --location --request GET 'https://www.googleapis.com/drive/v3/files' \
  --header 'Authorization: Bearer $ACCESS_TOKEN' \
  --header 'Accept: application/json'
  ```
- To be able to query cloud stroage, credentials may to be provided as a key file. The environment variable `GOOGLE_APPLICATION_CREDENTIALS` points to this file. For instructions on setting this up, refer to [link](https://cloud.google.com/storage/docs/reference/libraries#setting_up_authentication)

## Database
- Tables are created automatically if they are not present. Below steps are for information purposes only.
- docker pull postgres
- Run the container
  - First time `docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres`. Subsequent runs `docker start postgres`
- Schema setup
  - `docker exec -it postgres /bin/bash`
  - `psql --u postgres`
  - `\c postgres`
  - Create tables
  ```
    CREATE TABLE IF NOT EXISTS Scans (
    id serial PRIMARY KEY,
    scan_type VARCHAR (50) NOT NULL,
    created_on TIMESTAMP NOT NULL,
    scan_start_time TIMESTAMP NOT NULL,
    scan_end_time TIMESTAMP
  );

  CREATE TABLE IF NOT EXISTS ScanData (
   id serial PRIMARY KEY,
   name VARCHAR(200),
   path VARCHAR(2000),
   size BIGINT,
   file_mod_time TIMESTAMP,
   md5hash VARCHAR(60),
   is_dir boolean,
   file_count INT,
   scan_id INT NOT NULL,
   FOREIGN KEY (scan_id)
      REFERENCES Scans (id)
  );

  CREATE TABLE IF NOT EXISTS messagemetadata (
	id serial PRIMARY KEY,
	message_id VARCHAR(200),
	thread_id VARCHAR(200),
	date VARCHAR(200),
	mail_from VARCHAR(500),
	mail_to VARCHAR(500),
	subject VARCHAR(2000),
	size_estimate BIGINT,
	labels VARCHAR(500),
	scan_id INT NOT NULL,
	FOREIGN KEY (scan_id)
		REFERENCES Scans (id)
  );
  ```  

  ## Query
  - Query to see top usages
  ```
  select substring(mail_from from '<(.*)>') as m_from, sum(size_estimate), count(*) as count, 
  count(distinct thread_id) as thread_count, count(distinct message_id) as message_count 
  from messagemetadata 
  where scan_id = 62 
  group by substring(mail_from from '<(.*)>') 
  order by 3 desc 
  limit 5;
  ```