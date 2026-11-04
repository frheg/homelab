# Proposed high-level architecture

This file contains a proposed high-level architecture overview of kratos.

## Specs of kratos

- CPU: Intel i5-14400f
- GPU: Nvidia RTX 5060ti 16GB
- System RAM: 32GB DDR5 RAM
- VRAM: 16GB GDDR7
- SSD: 1.6TB SSD
- Operating System: NixOS

## Service overview

### Core service: Kubernetes

Kratos runs Kubernetes as k3s with monitoring and other related services as a core service - at top level.
This orchestrates all services under this tier.

### Other services running via k3s.

#### HomeLab/Chatbot - dependent on other services

A homemade chatbot or assistant to talk to and do stuff with locally running services and LLMs with tools and APIs etc.

#### Thesis-lab - dependent on other services 

Homemade lab for my mastersthesis running a lot of different services under it again, perhaps with these tier, albeit they might change into the new version 2 implementation:

- Lab: Highest level litterature search and source research with web access - with no access to local sensitive data.
- Enclave: Closed box inside of the Lab with access to read all content generated from Lab, but no communication out to the Lab. Has access to sensitive data in order to further work with relations between the actual working sensitive data and the found research from Lab.
- Bench: Closed throwaway containers within the Enclave where the Enclave can test and experiment and reason about the sensitive data and the found researched data from the Lab in safe environments with availability of training models on GPU and do other tests and implementations.

#### GPU Controller - not dependent on other services

Controls what has access to the GPU, if it is model training or LLM running or what not.

#### llama.cpp server - not dependent on other services

A llama.cpp instance with different models available for requests. Where a server that want to use llama.cpp says what kind of config it want (what models etc.) and it warms up on the GPU with a connection. Where I have multiple different models with different descriptions of use - not only qwen 3.8 27B and the others, but maybe some smaller models and some MoE models etc.

#### Strata server - not dependent on other services

A Strata server instance, similar to llama.cpp, where a service can request usage of it.

#### Any other additional inference engines - not dependent on other services

Perhaps there may be other inference engines I would want to test out or run at a later date, like FreeToken or maybe new ones that are not created yet.

#### Extendability of creating new services later - may/may not be dependent on other services, depends on what it is

Ease of extending or adding new services running on my k3s cluster.

#### Qdrant - not dependent on other services, but other services depend on it

Currently only used for homelab, but could perhaps be extended later with more collections for other uses.

#### Neo4j - not dependent on other services, but other services depend on it

Currently only used for homelab, but could perhaps be extended later with more collections for other uses.

#### PostgreSQL - not dependent on other services, but other services depend on it

Currently only used for homelab I think, but could perhaps be extended later with more collections for other uses.

The PostgreSQL instance for the sensitive data in the enclave regarding the Thesis-lab should be discusses as it is sensitive and important - should this be strictly inside the thesis-lab pod or the enclave pod, or a separate PostgreSQL instance?

## General

Is it possible to have the UIs of all services running at all times if it uses minimal RAM and general resources, where the backends are not in use when service in not in use, but spins up when UI is used, this is e.g. for the HomeLab/Chatbot? Also think we should rename the old HomeLab to chatbot or something like it, I want it to be my own kind of Jarvis as I plan on extending it so that I could talk to it at a later date with STT models etc. 

