import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.*
import jetbrains.buildServer.configs.kotlin.triggers.*

version = "2024.03"

const val IMAGE = "registry.acme.io/orders"

project {
    description = "Orders API"
    buildType(Build)
    buildType(Image)
    buildType(IntegrationTests)
    buildType(ContractTests)
    subProject(Deploy)
}

object Build : BuildType({
    name = "Build & unit test"
    artifactRules = """
        +:bin/orders => orders-bin.zip
        +:reports/** => reports.zip
    """.trimIndent()
    steps {
        script {
            name = "ci"
            scriptContent = "make ci"
        }
    }
    dependencies {
        artifacts(AbsoluteId("SharedLibs_PublishGo")) {
            artifactRules = "acme-go-sdk.tar.gz => third_party/"
        }
    }
    triggers {
        vcs { branchFilter = "+:*" }
    }
})

object Image : BuildType({
    name = "Docker image"
    params {
        param("image.tag", "%build.number%")
    }
    steps {
        script {
            name = "build & push"
            scriptContent = "make image push IMAGE=$IMAGE TAG=%image.tag%"
        }
    }
    dependencies {
        dependency(Build) {
            snapshot {}
            artifacts { artifactRules = "orders-bin.zip!** => bin/" }
        }
        snapshot(AbsoluteId("PlatformImages_BaseGo")) {}
    }
})

object IntegrationTests : BuildType({
    name = "Integration tests"
    steps {
        script {
            scriptContent = "./ci/integration.sh"
        }
    }
    dependencies {
        snapshot(Image) {}
    }
})

object ContractTests : BuildType({
    name = "Contract tests (Pact)"
    steps {
        script {
            scriptContent = "make pact-verify"
        }
    }
    dependencies {
        snapshot(Image) {}
        snapshot(AbsoluteId("PaymentsService_Image")) {}
    }
})
