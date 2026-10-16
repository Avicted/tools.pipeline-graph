import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.*
import jetbrains.buildServer.configs.kotlin.triggers.vcs

version = "2024.03"

project {
    description = "Shared Go and Kotlin client libraries"
    buildType(Verify)
    buildType(PublishGo)
    buildType(PublishKotlin)
}

object Verify : BuildType({
    name = "Verify libraries"
    steps {
        script {
            name = "lint + test"
            scriptContent = "./ci/verify.sh"
        }
    }
    triggers {
        vcs { branchFilter = "+:*" }
    }
})

object PublishGo : BuildType({
    name = "Publish Go SDK"
    artifactRules = "dist/acme-go-sdk.tar.gz"
    steps {
        script {
            scriptContent = "make -C go release VERSION=%build.number%"
        }
    }
    dependencies {
        snapshot(Verify) {}
        snapshot(AbsoluteId("PlatformImages_BaseGo")) {}
    }
})

object PublishKotlin : BuildType({
    name = "Publish Kotlin SDK"
    artifactRules = "kotlin/build/libs/*.jar"
    steps {
        gradle {
            name = "publish"
            tasks = ":sdk:publish"
            workingDir = "kotlin"
        }
    }
    dependencies {
        snapshot(Verify) {}
    }
})
