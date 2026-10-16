import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.script
import jetbrains.buildServer.configs.kotlin.triggers.schedule
import jetbrains.buildServer.configs.kotlin.triggers.vcs

version = "2024.03"

const val REGISTRY = "registry.acme.io"

project {
    description = "Hardened base images used by every service"

    template(ImageTemplate)
    buildType(LintDockerfiles)
    buildType(BaseGo)
    buildType(BaseNode)
    buildType(BaseJre)
    buildType(SecurityScan)
}

object ImageTemplate : Template({
    name = "Base image template"
    params {
        param("env.REGISTRY", REGISTRY)
    }
    triggers {
        vcs {
            branchFilter = "+:<default>"
        }
    }
    dependencies {
        snapshot(LintDockerfiles) {}
    }
})

object LintDockerfiles : BuildType({
    name = "Lint Dockerfiles"
    steps {
        script {
            name = "hadolint"
            scriptContent = "make lint"
        }
    }
})

object BaseGo : BuildType({
    templates(ImageTemplate)
    id("BaseGo")
    name = "Base image: Go"
    steps {
        script {
            name = "build & push"
            scriptContent = "make image-go push-go"
        }
    }
})

object BaseNode : BuildType({
    templates(ImageTemplate)
    id("BaseNode")
    name = "Base image: Node"
    steps {
        script {
            name = "build & push"
            scriptContent = "make image-node push-node"
        }
    }
})

object BaseJre : BuildType({
    templates(ImageTemplate)
    id("BaseJre")
    name = "Base image: JRE"
    steps {
        script {
            name = "build & push"
            scriptContent = "make image-jre push-jre"
        }
    }
})

object SecurityScan : BuildType({
    name = "Nightly CVE scan"
    steps {
        script {
            scriptContent = """
                docker pull $REGISTRY/base/go:latest
                docker pull $REGISTRY/base/node:latest
                docker pull $REGISTRY/base/jre:latest
                make scan
            """.trimIndent()
        }
    }
    triggers {
        schedule {
            schedulingPolicy = daily {
                hour = 3
            }
            branchFilter = "+:<default>"
        }
    }
    dependencies {
        snapshot(BaseGo) {}
        snapshot(BaseNode) {}
        snapshot(BaseJre) {}
    }
})
