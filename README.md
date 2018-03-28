# 6.857Coin

This is the blockchain server for 6.857Coin, a simple digital coin.

This project was started by David back in 2015.

- 2015 repo: https://github.com/davidlazar/6.857coin
- 2016 repo: https://github.com/cfromknecht/857coin
- 2017 repo: https://github.com/dpxcc/857coin-2017
- 2018 repo: https://github.com/dpxcc/857coin-2018

## Usage

1. Install:
   
        $ git clone https://github.com/dpxcc/857coin-2018.git
        $ go get github.com/syndtr/goleveldb/...

2. Create required directories:

        $ cd 857coin-2018
        $ mkdir logs

4. Run the blockchain server:

        $ go run server.go

5. Build a miner using the API described at http://localhost:8080
