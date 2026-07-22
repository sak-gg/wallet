# Wallet Service — Future Scope Plan


## 1. SDK for easier integration

In future, if multiple services need to integrate the Wallet servive, it can be repititive for each service. So, the owners of Wallet service can write and provide an SDK with standard usage and integration implementation, which can then be used uniformly by different clients.

---

## 2. Rate limiting on APIs

We can apply rate limiting on APIs to prevent attacks/abuse. Rate limiting can be implemented inside this service, or in API gateway, or through a common org-wide implementation of rate limiter.

---

## 3. Retry mechanisms

We can implement retry mechanisms on APIs in this service to make the requests fault tolerant. Exponential backoffs can be used to avoid bombardment of requests.

---

## 4. BDD scenarios

We can add support for BDD tests, if we want non-engineers to be adding test cases to our service in simple business language.

---
