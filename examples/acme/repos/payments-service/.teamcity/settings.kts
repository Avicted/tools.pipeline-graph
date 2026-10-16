import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.*
import jetbrains.buildServer.configs.kotlin.triggers.*

version = "2024.03"

const val IMAGE = "registry.acme.io/payments"

project {
    description = "Payments service (Kotlin / Spring)"
    buildType(Build)
    buildType(Image)
    buildType(DeployStaging)
    buildType(DeployProd)
}

// Every service build shares these settings.
open class ServiceBuild(title: String, init: BuildType.() -> Unit = {}) : BuildType({
    name = "payments: $title"
    params {
        param("env.GRADLE_OPTS", "-Xmx2g")
    }
    init(this)
})

fun Dependencies.needsKotlinSdk() {
    dependency(AbsoluteId("SharedLibs_PublishKotlin")) {
        snapshot {}
        artifacts { artifactRules = "*.jar => libs/" }
    }
}

object Build : ServiceBuild("build & test", {
    id("Build")
    artifactRules = "build/libs/payments.jar"
    steps {
        gradle {
            tasks = "clean build"
        }
    }
    dependencies {
        needsKotlinSdk()
    }
    triggers {
        vcs { branchFilter = "+:*" }
    }
})

object Image : ServiceBuild("docker image", {
    id("Image")
    steps {
        dockerCommand {
            name = "build"
            commandType = build {
                source = file { path = "docker/Dockerfile" }
                namesAndTags = "$IMAGE:%build.number%"
            }
        }
        dockerCommand {
            name = "push"
            commandType = push {
                namesAndTags = "$IMAGE:%build.number%"
            }
        }
    }
    dependencies {
        dependency(Build) {
            snapshot {}
            artifacts { artifactRules = "payments.jar => build/libs/" }
        }
        snapshot(AbsoluteId("PlatformImages_BaseJre")) {}
    }
})

object DeployStaging : ServiceBuild("deploy staging", {
    id("Deploy_staging")
    steps {
        script { scriptContent = "./scripts/deploy.sh staging" }
    }
    dependencies {
        snapshot(Image) {}
        snapshot(AbsoluteId("InfraTerraform_Apply_staging")) {}
    }
    triggers {
        finishBuildTrigger {
            buildType = "${Image.id}"
            successfulOnly = true
        }
    }
})

object DeployProd : ServiceBuild("deploy prod", {
    id("Deploy_prod")
    steps {
        script { scriptContent = "./scripts/deploy.sh prod" }
    }
    dependencies {
        snapshot(DeployStaging) {}
        snapshot(AbsoluteId("InfraTerraform_Apply_prod")) {}
    }
})
