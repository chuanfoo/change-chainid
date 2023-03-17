# change evmos chainId info
### change evmos chainId to a new one for purpose of test
### usage
1. change-chainid -home [chain data dir] -id [new chainId] -k [keysfile(keep one key per line)]

- -home: chain data
- -id: new chain id
- -k: a file contains all validator's key (in data/config/priv_validator_key.json);keep one key per line

### build
- go >= 1.18
```
go build -o change-chainid main.go
```
