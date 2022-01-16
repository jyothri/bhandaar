# hdd

## Docker build
- To build the image execute the following commands. (Execute at the *base* and not inside the build directory)
  ```
  docker build . -f .\build\Dockerfile -t jyothri/hdd-build
  ```
- To run the built image
  ```
  docker run -it --rm --name hdd-svelte -p 8080:8080 jyothri/hdd-build
  ```

## Parsing setup
- To be able to query Google drive, credentials are provided in the form of OAuth2 token. see [this](https://stackoverflow.com/a/35611334/6487201) answer for instructions. The steps include
  - Setup an oauth client & configure OAuth consent screen. May need to add specific gmail accounts for testing.
  - Obtain authorization code. E.g. https://accounts.google.com/o/oauth2/v2/auth?response_type=code&scope=https://www.googleapis.com/auth/drive.readonly&client_id=CLIENT_ID&state=YOUR_CUSTOM_STATE&redirect_uri=https://local.jkurapati.com&access_type=offline&prompt=consent
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
  ```

## Kinks
- Directory storage size is inconsistent. Consider a sample directory tree
```
test
├── folder1
│   └── test_file.txt (5)
└── folder2
    └── file2.txt  (1)
    └── folder3
        ├── another_file.txt (3)
        └── test_file.txt    (4)
```
- Local stores directory size information recursively.

|directory | size|
|----------|------|
|folder3   | 7 |
|folder2   | 8|
|folder1   | 5|
|test      | 13|

- google drive & cloud storage only save it at directory level excluding sub-directories

|directory | size|
|----------|----|
|folder3   | 7 |
|folder2   | 1|
|folder1   | 5|
|test      | 0|
