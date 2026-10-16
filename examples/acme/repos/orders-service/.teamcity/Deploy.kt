import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.script
import jetbrains.buildServer.configs.kotlin.triggers.finishBuildTrigger

fun deployTo(env: String, auto: Boolean) = BuildType {
    id("Deploy_${env}")
    name = "Deploy orders → $env"
    steps {
        script {
            name = "helm upgrade"
            scriptContent = "make deploy ENV=$env"
        }
    }
    dependencies {
        snapshot(IntegrationTests) {}
        snapshot(AbsoluteId("InfraTerraform_Apply_${env}")) {}
    }
    if (auto) {
        triggers {
            finishBuildTrigger {
                buildType = "${IntegrationTests.id}"
                successfulOnly = true
                branchFilter = "+:<default>"
            }
        }
    }
}

object Deploy : Project({
    name = "Deploy"
    sequential {
        buildType(deployTo("staging", true))
        buildType(deployTo("prod", false))
    }
})
