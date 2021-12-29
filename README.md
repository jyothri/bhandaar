# hdd

## Setup
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
