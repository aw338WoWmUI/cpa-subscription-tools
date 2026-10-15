#!/usr/bin/env python3
"""Keep the existing Dashboard usage history collector independent of routing."""
import os
import urllib.request

request = urllib.request.Request(
    "http://127.0.0.1:3000/api/usage/collect",
    data=b"{}",
    method="POST",
    headers={"Authorization": "Bearer " + os.environ["COLLECTOR_API_KEY"],
             "Content-Type": "application/json"},
)
with urllib.request.urlopen(request, timeout=90) as response:
    print("usage_collection_http=" + str(response.status))
