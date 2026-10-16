import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.dockerCommand
import jetbrains.buildServer.configs.kotlin.buildSteps.script
import jetbrains.buildServer.configs.kotlin.triggers.vcs

version = "2024.03"

const val REGISTRY = "registry.acme.io"

project {
    description = "Shared base images and tooling"

    template(CommonTemplate)
    buildType(Lint)
    buildType(BuildBaseImage)
}

object CommonTemplate : Template({
    name = "Common"
    params {
        param("env.REGISTRY", REGISTRY)
    }
    triggers {
        vcs {
            branchFilter = """
                +:<default>
                +:release/*
            """.trimIndent()
        }
    }
})

object Lint : BuildType({
    templates(CommonTemplate)
    name = "Lint"
    steps {
        script {
            name = "lint"
            scriptContent = "make lint"
        }
    }
})

object BuildBaseImage : BuildType({
    templates(CommonTemplate)
    id("BuildBaseImage")
    name = "Build base image"
    description = "Builds and pushes the shared base image"

    steps {
        script {
            name = "prepare"
            scriptContent = """
                #!/bin/bash
                set -euo pipefail
                ./ci/prepare.sh
            """.trimIndent()
        }
        dockerCommand {
            name = "build"
            commandType = build {
                source = file {
                    path = "Dockerfile"
                }
                namesAndTags = "$REGISTRY/base:%build.number%"
            }
        }
        dockerCommand {
            name = "push"
            commandType = push {
                namesAndTags = "$REGISTRY/base:%build.number%"
            }
        }
    }
    dependencies {
        snapshot(Lint) {}
    }
})
