# boberto
Boberto is a CLI coding agent

## features

- read a prd and iterate until done
- openai and anthropic api compatible
- can operate with or without a reviewer model
- baked in agent tools for basic needs: readfile, glob, grep, writefile

## operating flow

- on each iteration the worker reads the prd, summary, feedback, and explores the project filesystem
- the worker then does the work until it thinks it is done
- worker outputs writes a summary file to the project root
- the reviewer reads the prd, summary, and changed files
- reviewer writes a feeback file to the project root with its findings
- conversation context is dumped and the next iteration begins

## cli arguments

- -l / --limit, max number of iterations, if omitted there is no hard limit and the iterations will only stop if the reviewer decides there is no feedback
- -i / --init, creates a prd.md template in the current directory
- -d / --debug, prints agent conversation to stdout

## safety

- the agent will not execute tools outside of the project directory
- outside of api calls to llm provider, the agent will not make network requests
