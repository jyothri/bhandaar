# hdd

## Docker build
- To build the image execute the following commands. (Execute at the *base* and not inside the build directory)
  ```
  docker build . -f ./build/Dockerfile -t jyothri/hdd-go-build
  ```
- To build and start the stack with database use docker compose. 
  - The google application credentials file should be present
in the host at `~/keys/gae_creds.json`
  - Set the credentials as environment variables in [docker-compose.yml](build/docker-compose.yml) file. (currently set as dummy values) <br />
    For more information on how to obtain these check [these steps](debug.md#creds).
    - OAUTH_CLIENT_ID
    - OAUTH_CLIENT_SECRET
    - REFRESH_TOKEN
  - Bring the stack up
    ```
    docker compose -f build/docker-compose.yml up
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
