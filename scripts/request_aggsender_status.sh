#!/bin/bash
curl -X POST http://localhost:33032/ -H "Con -application/json"  -d '{"method":"aggsender_status", "params":[], "id":1}' | jq .
